from pathlib import Path
import re
root=Path('/tmp/src/MoonMigration')

# Version
p=root/'app/build.gradle.kts'; s=p.read_text(); s=s.replace('versionCode = 4','versionCode = 5').replace('versionName = "1.2.0"','versionName = "1.3.0"'); p.write_text(s)

# Models
p=root/'app/src/main/java/dev/moonmigration/app/Models.kt'; s=p.read_text();
s=s.replace('    val moonPosition: MoonPosition?,','    var moonPosition: MoonPosition?,')
s=s.replace('    val includedInBackup: Boolean = false\n)', '    var includedInBackup: Boolean = false,\n    var coverCachePath: String? = null,\n    val coverSourcePath: String? = null,\n    var isbn: String? = null,\n    var embeddedSize: Long? = null\n)')
p.write_text(s)

# Importer with robust path matching, cover extraction and progress callback.
p=root/'app/src/main/java/dev/moonmigration/app/MrproImporter.kt'
p.write_text(r'''package dev.moonmigration.app

import android.content.Context
import android.database.Cursor
import android.database.sqlite.SQLiteDatabase
import android.net.Uri
import android.util.Xml
import java.io.ByteArrayOutputStream
import java.io.File
import java.io.FileOutputStream
import java.util.Locale
import java.util.zip.ZipFile
import java.util.zip.ZipInputStream
import org.xmlpull.v1.XmlPullParser

data class MrproProgress(val stage: Stage, val current: Int = 0, val total: Int = 0) {
    enum class Stage { OPENING, READING_DATABASE, SCANNING_CONTENT, FINALIZING }
}

object MrproImporter {
    private const val MAX_DB_BYTES = 256L * 1024 * 1024
    private const val MAX_NAMES_BYTES = 32L * 1024 * 1024
    private const val MAX_POSITIONS_BYTES = 32L * 1024 * 1024
    private const val MAX_COVER_BYTES = 12L * 1024 * 1024
    private const val MAX_EPUB_FOR_EXACT_CFI = 768L * 1024 * 1024
    private const val MAX_ENTRIES = 200_000

    fun importBackup(context: Context, uri: Uri, isCancelled: () -> Boolean, onProgress: (MrproProgress) -> Unit = {}): List<BookEntry> {
        onProgress(MrproProgress(MrproProgress.Stage.OPENING))
        val cache = File(context.cacheDir, "moon-migration-import").apply { deleteRecursively(); mkdirs() }
        val dbFile = File(cache, "moon.sqlite")
        try {
            val names = extractDatabaseAndNames(context, uri, dbFile, isCancelled)
            onProgress(MrproProgress(MrproProgress.Stage.READING_DATABASE))
            val db = SQLiteDatabase.openDatabase(dbFile.path, null, SQLiteDatabase.OPEN_READONLY)
            val books = db.use { readBooks(it, isCancelled) }.toMutableList()
            val mapper = BackupNameMapper(names)
            for (book in books) {
                book.embeddedTag = mapper.tagFor(book.sourceFilename)
                book.includedInBackup = book.embeddedTag != null
            }
            val positionsTag = mapper.tagForSuffix("/shared_prefs/positions10.xml")
            val coverTags = linkedMapOf<String, MutableList<Int>>()
            books.forEachIndexed { i, b -> mapper.tagFor(b.coverSourcePath)?.let { coverTags.getOrPut(it){ mutableListOf() }.add(i) } }
            val bookTags = linkedMapOf<String, MutableList<Int>>()
            books.forEachIndexed { i, b -> b.embeddedTag?.let { bookTags.getOrPut(it){ mutableListOf() }.add(i) } }
            val wanted = (coverTags.keys + bookTags.keys + listOfNotNull(positionsTag)).toSet()
            scanContent(context, uri, books, positionsTag, coverTags, bookTags, wanted, cache, isCancelled, onProgress)
            onProgress(MrproProgress(MrproProgress.Stage.FINALIZING))
            return books.sortedBy { it.displayName.lowercase(Locale.ROOT) }
        } finally { dbFile.delete() }
    }

    private fun extractDatabaseAndNames(context: Context, uri: Uri, dbTarget: File, isCancelled: () -> Boolean): List<String> {
        val input = context.contentResolver.openInputStream(uri) ?: error("Backup unavailable")
        var entries=0; var names: List<String>?=null; var dbFound=false
        ZipInputStream(input.buffered(256*1024)).use { zip ->
            while (true) {
                checkCancelled(isCancelled); val e=zip.nextEntry ?: break
                entries++; require(entries<=MAX_ENTRIES){"Too many backup entries"}
                val clean=safeEntryName(e.name); if(e.isDirectory) continue
                when {
                    clean.endsWith("/_names.list") -> names=readLimited(zip,MAX_NAMES_BYTES).toString(Charsets.UTF_8).lineSequence().map{it.trim()}.filter{it.isNotEmpty()}.toList()
                    clean.substringAfterLast('/')=="1.tag" -> { copyLimited(zip,dbTarget,MAX_DB_BYTES); dbFound=true }
                }
            }
        }
        require(dbFound && dbTarget.length()>16){"Moon+ database not found"}
        dbTarget.inputStream().use { source -> val h=ByteArray(16); require(source.read(h)==16 && String(h,Charsets.US_ASCII)=="SQLite format 3\u0000"){"Invalid Moon+ database"} }
        return names ?: error("Moon+ backup file map not found")
    }

    private fun readBooks(db: SQLiteDatabase, isCancelled: () -> Boolean): List<BookEntry> {
        require(tableExists(db,"books") && tableExists(db,"notes")){"Unsupported Moon+ database"}
        val notes=linkedMapOf<String,MutableList<AnnotationRecord>>()
        db.rawQuery("SELECT filename, lowerFilename, lastChapter, lastSplitIndex, lastPosition, highlightLength, time, bookmark, note, original FROM notes ORDER BY time",null).use { c ->
            while(c.moveToNext()) { checkCancelled(isCancelled); val key=norm(c.str("lowerFilename")?:c.str("filename")?:continue); notes.getOrPut(key){mutableListOf()}.add(AnnotationRecord(c.longOrNull("time"),c.intOrNull("lastChapter"),c.intOrNull("lastSplitIndex"),c.intOrNull("lastPosition"),c.intOrNull("highlightLength"),c.str("note"),c.str("original"),c.str("bookmark"))) }
        }
        val out=mutableListOf<BookEntry>()
        db.rawQuery("SELECT book, filename, lowerFilename, author, description, coverFile, thumbFile FROM books ORDER BY book COLLATE NOCASE",null).use { c ->
            while(c.moveToNext()) {
                checkCancelled(isCancelled)
                val filename=c.str("filename"); val lower=c.str("lowerFilename"); val key=norm(lower?:filename?:c.str("book")?:continue)
                val title=c.str("book")?.trim().takeUnless{it.isNullOrBlank()} ?: filename?.substringAfterLast('/')?.substringBeforeLast('.') ?: "Unresolved book"
                val cover=(c.str("coverFile")?.takeIf{it.isNotBlank()} ?: c.str("thumbFile")?.takeIf{it.isNotBlank()})?.take(768)
                out += BookEntry(key,title.take(200),null,null,null,quality=PositionQuality.UNRESOLVED,author=c.str("author")?.take(200),sourceFilename=filename?.take(768),annotations=notes[key].orEmpty(),coverSourcePath=cover,isbn=findIsbn(c.str("description")))
            }
        }
        return out.distinctBy{it.key}
    }

    private fun scanContent(context: Context, uri: Uri, books: MutableList<BookEntry>, positionsTag: String?, coverTags: Map<String,List<Int>>, bookTags: Map<String,List<Int>>, wanted: Set<String>, cache: File, isCancelled: () -> Boolean, onProgress: (MrproProgress)->Unit) {
        if(wanted.isEmpty()) return
        val input=context.contentResolver.openInputStream(uri) ?: error("Backup unavailable")
        var entries=0; var found=0; val total=wanted.size.coerceAtLeast(1)
        ZipInputStream(input.buffered(256*1024)).use { zip ->
            while(true) {
                checkCancelled(isCancelled); val e=zip.nextEntry ?: break
                entries++; require(entries<=MAX_ENTRIES){"Too many backup entries"}; if(e.isDirectory) continue
                val tag=safeEntryName(e.name).substringAfterLast('/'); if(tag !in wanted) continue
                found++; onProgress(MrproProgress(MrproProgress.Stage.SCANNING_CONTENT,found,total))
                if(tag==positionsTag) {
                    val positions=parsePositions(readLimited(zip,MAX_POSITIONS_BYTES))
                    books.forEach { b -> val p=positions[norm(b.sourceFilename ?: b.key)] ?: positions[b.key]; if(p!=null){ b.moonPosition=p; b.quality=if(p.page!=null) PositionQuality.EXACT else if(p.percent!=null) PositionQuality.PERCENTAGE else PositionQuality.UNRESOLVED } }
                    continue
                }
                coverTags[tag]?.let { indexes ->
                    val bytes=try{readLimited(zip,MAX_COVER_BYTES)}catch(_:IllegalArgumentException){ByteArray(0)}
                    if(bytes.isNotEmpty()) for(i in indexes) { val f=File(cache,"cover-$i.img"); f.writeBytes(bytes); books[i].coverCachePath=f.path }
                    continue
                }
                bookTags[tag]?.let { indexes ->
                    val first=indexes.firstOrNull() ?: return@let
                    val book=books[first]; book.embeddedSize=e.size.takeIf{it>=0}
                    if(book.sourceFilename.orEmpty().lowercase(Locale.ROOT).endsWith(".epub") && e.size<=MAX_EPUB_FOR_EXACT_CFI) {
                        val temp=File(cache,"book-$first.epub")
                        try {
                            copyLimited(zip,temp,MAX_EPUB_FOR_EXACT_CFI)
                            val meta=readEpubMetadata(temp)
                            for(i in indexes) { if(books[i].isbn==null) books[i].isbn=meta.first; if(books[i].coverCachePath==null && meta.second!=null){ val cf=File(cache,"cover-$i.jpg"); cf.writeBytes(meta.second!!); books[i].coverCachePath=cf.path } }
                            book.moonPosition?.let { pos -> EpubPositionResolver.resolve({temp.inputStream()},temp.length(),pos)?.let { r -> for(i in indexes){books[i].cfi=r.cfi; books[i].quality=r.quality} } }
                        } catch(_:Throwable) { } finally { temp.delete() }
                    }
                }
            }
        }
    }

    private fun readEpubMetadata(file: File): Pair<String?,ByteArray?> {
        return try {
            ZipFile(file).use { z ->
                val container=z.getEntry("META-INF/container.xml") ?: return null to null
                val c=z.getInputStream(container).bufferedReader().use{it.readText()}
                val opfPath=Regex("full-path\\s*=\\s*[\"']([^\"']+)",RegexOption.IGNORE_CASE).find(c)?.groupValues?.get(1) ?: return null to null
                val opfEntry=z.getEntry(opfPath) ?: return null to null
                val opf=z.getInputStream(opfEntry).bufferedReader().use{it.readText()}
                val isbn=findIsbn(opf)
                val coverId=Regex("<meta[^>]+name=[\"']cover[\"'][^>]+content=[\"']([^\"']+)",RegexOption.IGNORE_CASE).find(opf)?.groupValues?.get(1)
                val href=coverId?.let { id -> Regex("<item[^>]+id=[\"']${Regex.escape(id)}[\"'][^>]+href=[\"']([^\"']+)",RegexOption.IGNORE_CASE).find(opf)?.groupValues?.get(1) }
                    ?: Regex("<item[^>]+properties=[\"'][^\"']*cover-image[^\"']*[\"'][^>]+href=[\"']([^\"']+)",RegexOption.IGNORE_CASE).find(opf)?.groupValues?.get(1)
                val base=opfPath.substringBeforeLast('/',"")
                val coverPath=href?.let { if(base.isBlank()) it else "$base/$it" }
                val cover=coverPath?.let { z.getEntry(it) }?.takeIf { it.size in 1..MAX_COVER_BYTES }?.let { z.getInputStream(it).use { input -> readLimited(input,MAX_COVER_BYTES) } }
                isbn to cover
            }
        } catch(_:Throwable) { null to null }
    }

    private fun parsePositions(bytes: ByteArray): Map<String,MoonPosition> {
        val out=linkedMapOf<String,MoonPosition>(); val p=Xml.newPullParser(); p.setFeature(XmlPullParser.FEATURE_PROCESS_NAMESPACES,false); p.setInput(bytes.inputStream(),"UTF-8"); var e=p.eventType
        while(e!=XmlPullParser.END_DOCUMENT){ if(e==XmlPullParser.START_TAG && p.name=="string"){ val n=p.getAttributeValue(null,"name"); val v=p.nextText(); if(!n.isNullOrBlank()) MoonParser.parseStoredPosition(v)?.let{out[norm(n)]=it} }; e=p.next() }; return out
    }

    private class BackupNameMapper(private val names: List<String>) {
        private val exact=linkedMapOf<String,String>(); private val byBase=linkedMapOf<String,MutableList<String>>()
        init { names.forEachIndexed { i,p -> val n=norm(p); val tag="${i+1}.tag"; exact[n]=tag; byBase.getOrPut(n.substringAfterLast('/')){mutableListOf()}.add(tag) } }
        fun tagFor(path: String?): String? {
            if(path.isNullOrBlank()) return null; val n=norm(path); exact[n]?.let{return it}
            exact.entries.firstOrNull { (k,_) -> k.endsWith("/$n") || n.endsWith("/$k") }?.value?.let{return it}
            return byBase[n.substringAfterLast('/')]?.singleOrNull()
        }
        fun tagForSuffix(suffix:String):String? = exact.entries.firstOrNull{it.key.endsWith(suffix.lowercase(Locale.ROOT))}?.value
    }

    private fun findIsbn(text:String?):String? { if(text.isNullOrBlank()) return null; return Regex("(?:ISBN(?:-1[03])?[:\\s]*)?((?:97[89][ -]?)?[0-9][0-9 -]{8,16}[0-9Xx])").findAll(text).map{it.groupValues[1].replace(" ","").replace("-","")}.firstOrNull{it.length==10||it.length==13} }
    private fun safeEntryName(raw:String):String { val clean=raw.replace('\\','/'); require(!clean.startsWith("/")&&clean.split('/').none{it==".."}){"Unsafe backup path"}; return clean }
    private fun readLimited(input:java.io.InputStream,maxBytes:Long):ByteArray { val out=ByteArrayOutputStream(); val b=ByteArray(64*1024); var total=0L; while(true){val n=input.read(b);if(n<0)break;total+=n;require(total<=maxBytes){"Backup entry exceeds safety limit"};out.write(b,0,n)};return out.toByteArray() }
    private fun copyLimited(input:java.io.InputStream,target:File,maxBytes:Long){FileOutputStream(target).use{out->val b=ByteArray(256*1024);var total=0L;while(true){val n=input.read(b);if(n<0)break;total+=n;require(total<=maxBytes){"Backup entry exceeds safety limit"};out.write(b,0,n)}}}
    private fun checkCancelled(f:()->Boolean){if(f())throw InterruptedException("cancelled")}
    private fun tableExists(db:SQLiteDatabase,name:String)=db.rawQuery("SELECT 1 FROM sqlite_master WHERE type='table' AND name=? LIMIT 1",arrayOf(name)).use{it.moveToFirst()}
    private fun norm(s:String)=s.replace('\\','/').lowercase(Locale.ROOT).trim()
    private fun Cursor.str(name:String):String?=getColumnIndex(name).takeIf{it>=0&&!isNull(it)}?.let(::getString)
    private fun Cursor.intOrNull(name:String):Int?=getColumnIndex(name).takeIf{it>=0&&!isNull(it)}?.let(::getInt)
    private fun Cursor.longOrNull(name:String):Long?=getColumnIndex(name).takeIf{it>=0&&!isNull(it)}?.let(::getLong)
}
''')

# MainActivity UI improvements.
p=root/'app/src/main/java/dev/moonmigration/app/MainActivity.kt'; s=p.read_text()
s=s.replace('import android.graphics.Typeface','import android.graphics.Typeface\nimport android.graphics.BitmapFactory\nimport java.util.Locale')
s=s.replace('import android.widget.LinearLayout','import android.widget.LinearLayout\nimport android.widget.ImageView')
s=s.replace('    private lateinit var sourceStatus: TextView','    private lateinit var sourceStatus: TextView\n    private lateinit var importProgress: ProgressBar\n    private lateinit var importProgressText: TextView')
old='''        root.addView(card(R.string.source_title, R.string.source_desc) { body ->\n            val pick = button(R.string.choose_source) { chooseSource.launch(arrayOf("application/octet-stream", "application/zip", "application/x-zip-compressed", "*/*")) }\n            body.addView(pick)\n            sourceStatus = secondaryText("")\n            body.addView(sourceStatus)\n            body.addView(button(R.string.scan) { scanBackup() })\n        })'''
new='''        root.addView(card(R.string.source_title, R.string.source_desc) { body ->\n            body.addView(button(R.string.choose_source) { chooseSource.launch(arrayOf("application/octet-stream", "application/zip", "application/x-zip-compressed", "*/*")) })\n            sourceStatus = secondaryText(getString(R.string.no_backup_selected))\n            body.addView(sourceStatus)\n            importProgressText = secondaryText("")\n            body.addView(importProgressText)\n            importProgress = ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal).apply { max=1000; visibility=View.GONE }\n            body.addView(importProgress, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(8)).apply { topMargin=dp(8) })\n        })'''
if old not in s: raise SystemExit('source card not found')
s=s.replace(old,new)
s=s.replace('sourceStatus.setText(R.string.source_selected)\n            scanBackup()', 'sourceStatus.text = getString(R.string.source_selected_file, queryDisplayName(uri) ?: getString(R.string.unknown_file))\n            scanBackup()')
s=s.replace('val found = MrproImporter.importBackup(this, uri) { cancelled.get() || Thread.currentThread().isInterrupted }', '''val found = MrproImporter.importBackup(this, uri, { cancelled.get() || Thread.currentThread().isInterrupted }) { p ->\n                    runOnUiThread { updateImportProgress(p) }\n                }''')
pat=re.compile(r'    private fun renderBooks\(\) \{.*?\n    \}\n\n    private fun testKoSync',re.S)
replacement=r'''    private fun renderBooks() {
        booksContainer.removeAllViews()
        booksEmpty.visibility = if (books.isEmpty()) View.VISIBLE else View.GONE
        books.forEachIndexed { index, book ->
            val card = MaterialCardView(this).apply {
                radius = dp(14).toFloat(); cardElevation = dp(1).toFloat(); setContentPadding(dp(12),dp(12),dp(12),dp(12))
            }
            val outer = LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL; gravity = Gravity.TOP }
            val cover = ImageView(this).apply {
                scaleType = ImageView.ScaleType.CENTER_CROP
                setBackgroundColor(0x11000000)
                val path=book.coverCachePath
                if(path!=null) BitmapFactory.decodeFile(path)?.let { setImageBitmap(it) }
            }
            outer.addView(cover, LinearLayout.LayoutParams(dp(82),dp(116)).apply { marginEnd=dp(12) })
            val details = LinearLayout(this).apply { orientation=LinearLayout.VERTICAL }
            details.addView(TextView(this).apply { text=book.displayName; textSize=17f; setTypeface(typeface,Typeface.BOLD) })
            book.author?.takeIf{it.isNotBlank()}?.let { details.addView(secondaryText(it)) }
            val progressLabel = book.moonPosition?.percent?.let { getString(R.string.reading_progress_fmt,it) } ?: getString(R.string.reading_progress_unknown)
            details.addView(TextView(this).apply { text=progressLabel; textSize=15f; setPadding(0,dp(6),0,0) })
            val q = when(book.quality){ PositionQuality.EXACT->getString(R.string.exact); PositionQuality.PERCENTAGE->getString(R.string.percentage); PositionQuality.UNRESOLVED->getString(R.string.unresolved) }
            details.addView(secondaryText(q))
            book.isbn?.let { details.addView(secondaryText(getString(R.string.isbn_fmt,it))) }
            val format=book.sourceFilename?.substringAfterLast('.')?.uppercase(Locale.getDefault())
            if(format!=null) details.addView(secondaryText(getString(R.string.format_fmt,format)))
            details.addView(secondaryText(if(book.includedInBackup) getString(R.string.book_file_in_backup) else getString(R.string.book_file_missing)))
            details.addView(button(if(book.ebookUri==null) R.string.assign_ebook else R.string.ebook_assigned) {
                pendingBookIndex=index; chooseEbook.launch(arrayOf("application/epub+zip","application/pdf","application/octet-stream"))
            })
            outer.addView(details,LinearLayout.LayoutParams(0,ViewGroup.LayoutParams.WRAP_CONTENT,1f))
            card.addView(outer)
            booksContainer.addView(card, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT,ViewGroup.LayoutParams.WRAP_CONTENT).apply { bottomMargin=dp(10) })
        }
    }

    private fun updateImportProgress(p: MrproProgress) {
        if (!::importProgress.isInitialized) return
        importProgress.visibility = View.VISIBLE
        when(p.stage) {
            MrproProgress.Stage.OPENING -> { importProgress.isIndeterminate=true; importProgressText.setText(R.string.import_opening) }
            MrproProgress.Stage.READING_DATABASE -> { importProgress.isIndeterminate=true; importProgressText.setText(R.string.import_database) }
            MrproProgress.Stage.SCANNING_CONTENT -> { importProgress.isIndeterminate=false; importProgress.progress=if(p.total<=0)0 else ((p.current.toDouble()/p.total)*1000).toInt(); importProgressText.text=getString(R.string.import_content_fmt,p.current,p.total) }
            MrproProgress.Stage.FINALIZING -> { importProgress.isIndeterminate=true; importProgressText.setText(R.string.import_finalizing) }
        }
    }

    private fun testKoSync'''
s,n=pat.subn(replacement,s,count=1)
if n!=1: raise SystemExit('renderBooks not replaced')
s=s.replace('books = found.toMutableList()\n                    renderBooks()\n                    finishOperation(getString(R.string.phase_done))', 'books = found.toMutableList()\n                    renderBooks()\n                    importProgress.isIndeterminate=false; importProgress.progress=1000; importProgressText.text=getString(R.string.import_done_fmt,books.size)\n                    finishOperation(getString(R.string.phase_done))')
p.write_text(s)

# Strings
for folder,de in [('values',False),('values-de',True)]:
    p=root/f'app/src/main/res/{folder}/strings.xml'; s=p.read_text()
    replacements = ({
      'source_desc':'Erstelle in Moon+ Reader eine vollständige Sicherung inklusive Buchdateien. Wähle anschließend hier genau die erzeugte .mrpro-Sicherungsdatei aus – keinen Ordner.',
      'choose_source':'Moon+ Sicherungsdatei (.mrpro) auswählen',
      'error_source':'Wähle zuerst eine konkrete .mrpro-Sicherungsdatei aus.',
      'books_empty':'Noch keine .mrpro-Sicherung importiert.'
    } if de else {
      'source_desc':'Create a complete Moon+ Reader backup including book files. Then select the exact .mrpro backup file here — not a folder.',
      'choose_source':'Select Moon+ backup file (.mrpro)',
      'error_source':'Select a specific .mrpro backup file first.',
      'books_empty':'No .mrpro backup imported yet.'
    })
    for key,val in replacements.items(): s=re.sub(fr'<string name="{key}">.*?</string>',f'<string name="{key}">{val}</string>',s)
    add = '''
    <string name="no_backup_selected">Noch keine Sicherungsdatei ausgewählt</string>
    <string name="source_selected_file">Ausgewählt: %1$s</string>
    <string name="unknown_file">Moon+ Sicherung</string>
    <string name="import_opening">Sicherungsdatei wird geöffnet und geprüft…</string>
    <string name="import_database">Buchdatenbank wird eingelesen…</string>
    <string name="import_content_fmt">Buchdateien, Cover und Lesestände werden verarbeitet: %1$d / %2$d</string>
    <string name="import_finalizing">Bücherliste wird aufgebaut…</string>
    <string name="import_done_fmt">Import abgeschlossen · %1$d Bücher erkannt</string>
    <string name="reading_progress_fmt">Lesefortschritt: %1$.1f%%</string>
    <string name="reading_progress_unknown">Lesefortschritt nicht ermittelt</string>
    <string name="isbn_fmt">ISBN: %1$s</string>
    <string name="format_fmt">Format: %1$s</string>
    <string name="book_file_in_backup">Buchdatei: in vollständiger Sicherung enthalten</string>
    <string name="book_file_missing">Buchdatei: nicht in Sicherung erkannt · kann manuell zugeordnet werden</string>
''' if de else '''
    <string name="no_backup_selected">No backup file selected</string>
    <string name="source_selected_file">Selected: %1$s</string>
    <string name="unknown_file">Moon+ backup</string>
    <string name="import_opening">Opening and validating backup file…</string>
    <string name="import_database">Reading book database…</string>
    <string name="import_content_fmt">Processing book files, covers and reading positions: %1$d / %2$d</string>
    <string name="import_finalizing">Building book list…</string>
    <string name="import_done_fmt">Import complete · %1$d books detected</string>
    <string name="reading_progress_fmt">Reading progress: %1$.1f%%</string>
    <string name="reading_progress_unknown">Reading progress not determined</string>
    <string name="isbn_fmt">ISBN: %1$s</string>
    <string name="format_fmt">Format: %1$s</string>
    <string name="book_file_in_backup">Book file: included in complete backup</string>
    <string name="book_file_missing">Book file: not detected in backup · can be assigned manually</string>
'''
    s=s.replace('</resources>',add+'</resources>'); p.write_text(s)
