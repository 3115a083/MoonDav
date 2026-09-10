from pathlib import Path
import base64, re
root = Path('/tmp/src/MoonMigration')

# Version bump.
b = root/'app/build.gradle.kts'
s = b.read_text()
s = s.replace('versionCode = 3', 'versionCode = 4').replace('versionName = "1.1.0"', 'versionName = "1.2.0"')
b.write_text(s)

# Model fields for full-backup provenance.
p = root/'app/src/main/java/dev/moonmigration/app/Models.kt'
s = p.read_text()
s = s.replace('    val annotations: List<AnnotationRecord> = emptyList()\n)', '    val annotations: List<AnnotationRecord> = emptyList(),\n    val embeddedTag: String? = null,\n    val includedInBackup: Boolean = false\n)')
p.write_text(s)

# Parse Moon+ positions10.xml values that do not carry the .po timestamp prefix.
p = root/'app/src/main/java/dev/moonmigration/app/MoonParser.kt'
s = p.read_text()
s = s.replace('    private val page = Regex("^(\\\\d+)\\\\*(\\\\d+):([0-9]+(?:\\\\.[0-9]+)?)%?$")', '    private val page = Regex("^(\\\\d+)\\\\*(\\\\d+):([0-9]+(?:\\\\.[0-9]+)?)%?$")\n    private val storedEpub = Regex("^(\\\\d+)@(\\\\d+)#(\\\\d+):([0-9]+(?:\\\\.[0-9]+)?)%?$")\n    private val storedPage = Regex("^(\\\\d+):([0-9]+(?:\\\\.[0-9]+)?)%?$")')
insert = '''\n\n    fun parseStoredPosition(input: String, timestamp: Long? = null): MoonPosition? {
        val raw = input.trim()
        parsePosition(raw)?.let { return it }
        storedEpub.matchEntire(raw)?.let { m ->
            return MoonPosition(raw, timestamp, m.groupValues[1].toIntOrNull(), m.groupValues[2].toIntOrNull(), m.groupValues[3].toIntOrNull(), null, normalizePercent(m.groupValues[4].toDoubleOrNull()))
        }
        storedPage.matchEntire(raw)?.let { m ->
            return MoonPosition(raw, timestamp, null, null, null, m.groupValues[1].toIntOrNull(), normalizePercent(m.groupValues[2].toDoubleOrNull()))
        }
        return null
    }
'''
s = s.replace('\n    fun inflateAnnotation', insert + '\n    fun inflateAnnotation')
p.write_text(s)

# Replace the old loose-file scanner path with a full .mrpro importer.
(root/'app/src/main/java/dev/moonmigration/app/MrproImporter.kt').write_text(r'''package dev.moonmigration.app

import android.content.Context
import android.database.Cursor
import android.database.sqlite.SQLiteDatabase
import android.net.Uri
import android.util.Xml
import java.io.ByteArrayOutputStream
import java.io.File
import java.io.FileOutputStream
import java.util.Locale
import java.util.zip.ZipInputStream
import org.xmlpull.v1.XmlPullParser

object MrproImporter {
    private const val MAX_DB_BYTES = 256L * 1024 * 1024
    private const val MAX_NAMES_BYTES = 16L * 1024 * 1024
    private const val MAX_POSITIONS_BYTES = 32L * 1024 * 1024
    private const val MAX_EPUB_FOR_EXACT_CFI = 512L * 1024 * 1024
    private const val MAX_ENTRIES = 100_000

    fun importBackup(context: Context, uri: Uri, isCancelled: () -> Boolean): List<BookEntry> {
        val dbFile = File.createTempFile("moon-migration-db-", ".sqlite", context.cacheDir)
        try {
            val names = extractDatabaseAndNames(context, uri, dbFile, isCancelled)
            val positionsTag = names.indexOfFirst { it.replace('\\', '/').lowercase(Locale.ROOT).endsWith("/shared_prefs/positions10.xml") }
                .takeIf { it >= 0 }?.let { "${it + 1}.tag" }
            val positions = positionsTag?.let { extractSmallEntry(context, uri, it, MAX_POSITIONS_BYTES, isCancelled) }?.let(::parsePositions).orEmpty()
            val embedded = linkedMapOf<String, String>()
            names.forEachIndexed { index, originalPath -> if (isBookPath(originalPath)) embedded[norm(originalPath)] = "${index + 1}.tag" }
            val db = SQLiteDatabase.openDatabase(dbFile.path, null, SQLiteDatabase.OPEN_READONLY)
            val books = db.use { readBooks(it, positions, embedded, isCancelled) }.toMutableList()
            resolveEmbeddedEpubs(context, uri, books, isCancelled)
            return books.sortedBy { it.displayName.lowercase(Locale.ROOT) }
        } finally { dbFile.delete() }
    }

    private fun extractDatabaseAndNames(context: Context, uri: Uri, dbTarget: File, isCancelled: () -> Boolean): List<String> {
        val input = context.contentResolver.openInputStream(uri) ?: error("Backup unavailable")
        var entries = 0; var dbFound = false; var names: List<String>? = null
        ZipInputStream(input.buffered()).use { zip ->
            while (true) {
                checkCancelled(isCancelled)
                val e = zip.nextEntry ?: break
                entries++; require(entries <= MAX_ENTRIES) { "Too many backup entries" }
                val clean = safeEntryName(e.name)
                if (e.isDirectory) continue
                when {
                    clean.endsWith("/1.tag") -> { copyLimited(zip, dbTarget, MAX_DB_BYTES); dbFound = true }
                    clean.endsWith("/_names.list") -> names = readLimited(zip, MAX_NAMES_BYTES).toString(Charsets.UTF_8).lineSequence().map { it.trim() }.filter { it.isNotEmpty() }.toList()
                }
            }
        }
        require(dbFound && dbTarget.length() > 16) { "Moon+ database not found" }
        dbTarget.inputStream().use { source ->
            val header = ByteArray(16)
            require(source.read(header) == 16 && String(header, Charsets.US_ASCII) == "SQLite format 3\u0000") { "Invalid Moon+ database" }
        }
        return names ?: error("Moon+ backup file map not found")
    }

    private fun extractSmallEntry(context: Context, uri: Uri, tagName: String, maxBytes: Long, isCancelled: () -> Boolean): ByteArray? {
        val input = context.contentResolver.openInputStream(uri) ?: error("Backup unavailable")
        ZipInputStream(input.buffered()).use { zip ->
            var entries = 0
            while (true) {
                checkCancelled(isCancelled)
                val e = zip.nextEntry ?: return null
                entries++; require(entries <= MAX_ENTRIES) { "Too many backup entries" }
                val clean = safeEntryName(e.name)
                if (!e.isDirectory && clean.substringAfterLast('/') == tagName) return readLimited(zip, maxBytes)
            }
        }
    }

    private fun parsePositions(bytes: ByteArray): Map<String, MoonPosition> {
        val result = linkedMapOf<String, MoonPosition>()
        val parser = Xml.newPullParser(); parser.setFeature(XmlPullParser.FEATURE_PROCESS_NAMESPACES, false); parser.setInput(bytes.inputStream(), "UTF-8")
        var event = parser.eventType
        while (event != XmlPullParser.END_DOCUMENT) {
            if (event == XmlPullParser.START_TAG && parser.name == "string") {
                val name = parser.getAttributeValue(null, "name"); val value = parser.nextText()
                if (!name.isNullOrBlank()) MoonParser.parseStoredPosition(value)?.let { result[norm(name)] = it }
            }
            event = parser.next()
        }
        return result
    }

    private fun readBooks(db: SQLiteDatabase, positions: Map<String, MoonPosition>, embedded: Map<String, String>, isCancelled: () -> Boolean): List<BookEntry> {
        require(tableExists(db, "books") && tableExists(db, "notes")) { "Unsupported Moon+ database" }
        val notes = linkedMapOf<String, MutableList<AnnotationRecord>>()
        db.rawQuery("SELECT filename, lowerFilename, lastChapter, lastSplitIndex, lastPosition, highlightLength, time, bookmark, note, original FROM notes ORDER BY time", null).use { c ->
            while (c.moveToNext()) {
                checkCancelled(isCancelled)
                val key = norm(c.str("lowerFilename") ?: c.str("filename") ?: continue)
                notes.getOrPut(key) { mutableListOf() }.add(AnnotationRecord(c.longOrNull("time"), c.intOrNull("lastChapter"), c.intOrNull("lastSplitIndex"), c.intOrNull("lastPosition"), c.intOrNull("highlightLength"), c.str("note"), c.str("original"), c.str("bookmark")))
            }
        }
        val result = mutableListOf<BookEntry>()
        db.rawQuery("SELECT book, filename, lowerFilename, author FROM books ORDER BY book COLLATE NOCASE", null).use { c ->
            while (c.moveToNext()) {
                checkCancelled(isCancelled)
                val filename = c.str("filename"); val lower = c.str("lowerFilename")
                val key = norm(lower ?: filename ?: c.str("book") ?: continue); val sourceKey = filename?.let(::norm) ?: key
                val position = positions[sourceKey] ?: positions[key]; val tag = embedded[sourceKey] ?: embedded[key]
                val title = c.str("book")?.trim().takeUnless { it.isNullOrBlank() } ?: filename?.substringAfterLast('/')?.substringBeforeLast('.') ?: "Unresolved book"
                val quality = when { position?.page != null -> PositionQuality.EXACT; position?.percent != null -> PositionQuality.PERCENTAGE; else -> PositionQuality.UNRESOLVED }
                result += BookEntry(key, title.take(160), null, null, position, quality = quality, author = c.str("author")?.take(160), sourceFilename = filename?.take(512), annotations = notes[key].orEmpty(), embeddedTag = tag, includedInBackup = tag != null)
            }
        }
        return result.distinctBy { it.key }
    }

    private fun resolveEmbeddedEpubs(context: Context, uri: Uri, books: MutableList<BookEntry>, isCancelled: () -> Boolean) {
        val byTag = books.withIndex().mapNotNull { (index, book) ->
            val tag = book.embeddedTag ?: return@mapNotNull null
            if (!book.sourceFilename.orEmpty().lowercase(Locale.ROOT).endsWith(".epub") || book.moonPosition?.chapter == null) return@mapNotNull null
            tag to index
        }.toMap()
        if (byTag.isEmpty()) return
        val input = context.contentResolver.openInputStream(uri) ?: error("Backup unavailable")
        ZipInputStream(input.buffered()).use { zip ->
            var entries = 0
            while (true) {
                checkCancelled(isCancelled)
                val e = zip.nextEntry ?: break
                entries++; require(entries <= MAX_ENTRIES) { "Too many backup entries" }
                val clean = safeEntryName(e.name); if (e.isDirectory) continue
                val index = byTag[clean.substringAfterLast('/')] ?: continue
                if (e.size > MAX_EPUB_FOR_EXACT_CFI) continue
                val temp = File.createTempFile("moon-migration-book-", ".epub", context.cacheDir)
                try {
                    try { copyLimited(zip, temp, MAX_EPUB_FOR_EXACT_CFI) } catch (_: IllegalArgumentException) { continue }
                    val book = books[index]; val pos = book.moonPosition ?: continue
                    EpubPositionResolver.resolve({ temp.inputStream() }, temp.length(), pos)?.let { book.cfi = it.cfi; book.quality = it.quality }
                } catch (_: Throwable) { } finally { temp.delete() }
            }
        }
    }

    private fun isBookPath(path: String): Boolean { val p = path.lowercase(Locale.ROOT); return listOf(".epub", ".pdf", ".mobi", ".azw", ".azw3", ".fb2", ".txt", ".cbz", ".cbr").any(p::endsWith) }
    private fun safeEntryName(raw: String): String { val clean = raw.replace('\\', '/'); require(!clean.startsWith("/") && clean.split('/').none { it == ".." }) { "Unsafe backup path" }; return clean }
    private fun readLimited(input: java.io.InputStream, maxBytes: Long): ByteArray { val out = ByteArrayOutputStream(); val buf = ByteArray(64*1024); var total=0L; while(true){ val n=input.read(buf); if(n<0) break; total+=n; require(total<=maxBytes){"Backup entry exceeds safety limit"}; out.write(buf,0,n) }; return out.toByteArray() }
    private fun copyLimited(input: java.io.InputStream, target: File, maxBytes: Long) { FileOutputStream(target).use { out -> val buf=ByteArray(64*1024); var total=0L; while(true){ val n=input.read(buf); if(n<0) break; total+=n; require(total<=maxBytes){"Backup entry exceeds safety limit"}; out.write(buf,0,n) } } }
    private fun checkCancelled(isCancelled: () -> Boolean) { if (isCancelled()) throw InterruptedException("cancelled") }
    private fun tableExists(db: SQLiteDatabase, name: String): Boolean = db.rawQuery("SELECT 1 FROM sqlite_master WHERE type='table' AND name=? LIMIT 1", arrayOf(name)).use { it.moveToFirst() }
    private fun norm(s: String) = s.replace('\\','/').lowercase(Locale.ROOT).trim()
    private fun Cursor.str(name: String): String? = getColumnIndex(name).takeIf { it >= 0 && !isNull(it) }?.let(::getString)
    private fun Cursor.intOrNull(name: String): Int? = getColumnIndex(name).takeIf { it >= 0 && !isNull(it) }?.let(::getInt)
    private fun Cursor.longOrNull(name: String): Long? = getColumnIndex(name).takeIf { it >= 0 && !isNull(it) }?.let(::getLong)
}
''')

# Main UI: choose .mrpro directly, start import automatically, correct folder picker, show included-book state.
p = root/'app/src/main/java/dev/moonmigration/app/MainActivity.kt'
s = p.read_text()
s = s.replace('sourceStatus.setText(R.string.source_selected)\n        }', 'sourceStatus.setText(R.string.source_selected)\n            scanBackup()\n        }', 1)
s = s.replace('registerForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->\n        if (uri != null) {\n            try { contentResolver.takePersistableUriPermission(uri, Intent.FLAG_GRANT_READ_URI_PERMISSION) } catch (_: SecurityException) {}\n            destinationUri = uri', 'registerForActivityResult(ActivityResultContracts.OpenDocumentTree()) { uri ->\n        if (uri != null) {\n            try { contentResolver.takePersistableUriPermission(uri, Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION) } catch (_: SecurityException) {}\n            destinationUri = uri', 1)
s = s.replace('book.moonPosition?.percent?.let { append(" · ").append(String.format("%.2f%%", it)) }\n            }', 'book.moonPosition?.percent?.let { append(" · ").append(String.format("%.2f%%", it)) }\n                if (book.includedInBackup) append(" · ").append(getString(R.string.book_in_backup))\n            }')
p.write_text(s)

# User-facing instructions: insist on complete Moon+ Reader backup including books.
for sub, de in [('values', False), ('values-de', True)]:
    p = root/f'app/src/main/res/{sub}/strings.xml'; s = p.read_text()
    if de:
        s = s.replace('Wähle eine Moon+ Reader-.mrpro-Sicherung. Die eingebettete Datenbank wird ausschließlich lokal gelesen.', 'Erstelle in Moon+ Reader eine VOLLSTÄNDIGE Sicherung inklusive Buchdateien und wähle die erzeugte .mrpro-Datei hier aus. Die App liest Datenbank, aktuelle Lesepositionen und enthaltene E-Books ausschließlich lokal. Auch Sicherungen über 4 GB werden unterstützt.')
        s = s.replace('Sicherung ausgewählt', 'Sicherung ausgewählt · Import startet automatisch')
        s = s.replace('Sicherung wird gescannt…', 'Vollständige Sicherung wird importiert… große Sicherungen können etwas dauern')
        s = s.replace('Die Sicherung konnte nicht gescannt werden.', 'Die Sicherung konnte nicht importiert werden. Prüfe, ob es eine vollständige Moon+ Reader-.mrpro-Sicherung ist.')
        s = s.replace('</resources>', '    <string name="book_in_backup">E-Book enthalten</string>\n</resources>')
    else:
        s = s.replace('Choose a Moon+ Reader .mrpro backup. The app reads its embedded database locally.', 'In Moon+ Reader, create a COMPLETE backup including book files, then select the resulting .mrpro file here. The app reads the database, current positions and included ebooks locally. Large backups over 4 GB are supported.')
        s = s.replace('Backup selected', 'Backup selected · import starts automatically')
        s = s.replace('Scanning backup…', 'Importing complete backup… large backups can take a while')
        s = s.replace('The backup could not be scanned.', 'The backup could not be imported. Make sure it is a complete Moon+ Reader .mrpro backup.')
        s = s.replace('</resources>', '    <string name="book_in_backup">ebook included</string>\n</resources>')
    p.write_text(s)

# HTTPS-only tests and positions10 parser tests.
p = root/'app/src/test/java/dev/moonmigration/app/UtilityTest.kt'
s = p.read_text()
extra = '''\n    @Test(expected = IllegalArgumentException::class) fun rejectsHttpEvenOnPrivateLan() { KoSyncClient.validateBaseUrl("http://192.168.1.20:8080", true, true) }\n    @Test fun parsesStoredEpubPosition() { val p = MoonParser.parseStoredPosition("24@0#846:99.9%"); assertEquals(24, p?.chapter); assertEquals(846, p?.characterOffset); assertEquals(99.9, p?.percent ?: -1.0, 0.0001) }\n    @Test fun parsesStoredPagePosition() { val p = MoonParser.parseStoredPosition("42:7.3%"); assertEquals(42, p?.page); assertEquals(7.3, p?.percent ?: -1.0, 0.0001) }\n'''
pos = s.rfind('}')
s = s[:pos] + extra + s[pos:]
p.write_text(s)

# Generated Moon-in-box icon supplied by the user-approved image generation result.
icon = base64.b64decode(Path('.build/moon_icon.b64').read_text().strip())
drawable = root/'app/src/main/res/drawable/moon_migration_icon.png'
drawable.write_bytes(icon)
p = root/'app/src/main/AndroidManifest.xml'; s = p.read_text()
s = re.sub(r'android:icon="[^"]+"', 'android:icon="@drawable/moon_migration_icon"', s)
s = re.sub(r'android:roundIcon="[^"]+"', 'android:roundIcon="@drawable/moon_migration_icon"', s)
p.write_text(s)

# KOSync remains HTTPS-only. Remove misleading HTTP-LAN labels if present; UI already supplies false.
for sub in ['values','values-de']:
    p = root/f'app/src/main/res/{sub}/strings.xml'; s = p.read_text(); s = re.sub(r'\s*<string name="kosync_allow_http_lan">.*?</string>', '', s); p.write_text(s)
