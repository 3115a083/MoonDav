from pathlib import Path
import re
root = Path('/tmp/src/MoonMigration')

# Bring the actual v1.1 model forward with .mrpro metadata and annotations.
models = root/'app/src/main/java/dev/moonmigration/app/Models.kt'
s = models.read_text()
start = s.index('data class BookEntry(')
s = s[:start] + '''data class AnnotationRecord(\n    val timestamp: Long?,\n    val chapter: Int?,\n    val splitIndex: Int?,\n    val position: Int?,\n    val length: Int?,\n    val note: String?,\n    val original: String?,\n    val bookmark: String?\n)\n\ndata class BookEntry(\n    val key: String,\n    val displayName: String,\n    val poUri: Uri?,\n    val anUri: Uri?,\n    val moonPosition: MoonPosition?,\n    var ebookUri: Uri? = null,\n    var ebookDisplayName: String? = null,\n    var cfi: String? = null,\n    var quality: PositionQuality = PositionQuality.UNRESOLVED,\n    val author: String? = null,\n    val sourceFilename: String? = null,\n    val annotations: List<AnnotationRecord> = emptyList(),\n    val embeddedTag: String? = null,\n    val includedInBackup: Boolean = false\n)\n'''
models.write_text(s)

# Make the .mrpro file itself the source, auto-import it, and remove the obsolete HTTP-LAN switch.
main = root/'app/src/main/java/dev/moonmigration/app/MainActivity.kt'
s = main.read_text()
s = s.replace('private lateinit var koAllowHttpLan: CheckBox\n', '')
source_block = re.compile(r'    private val chooseSource = registerForActivityResult\(ActivityResultContracts\.OpenDocumentTree\(\)\) \{ uri ->.*?\n    \}\n', re.S)
replacement = '''    private val chooseSource = registerForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->\n        if (uri != null) {\n            try { contentResolver.takePersistableUriPermission(uri, Intent.FLAG_GRANT_READ_URI_PERMISSION) } catch (_: SecurityException) { }\n            sourceUri = uri\n            sourceStatus.setText(R.string.source_selected)\n            scanBackup()\n        }\n    }\n'''
s, n = source_block.subn(replacement, s, count=1)
if n != 1: raise SystemExit('chooseSource launcher patch did not apply')
s = re.sub(r'val pick = button\(R\.string\.choose_source\) \{ chooseSource\.launch\([^\n]*\) \}', 'val pick = button(R.string.choose_source) { chooseSource.launch(arrayOf("application/octet-stream", "application/zip", "application/x-zip-compressed", "*/*")) }', s, count=1)
s = s.replace('            koAllowHttpLan = CheckBox(this).apply { setText(R.string.kosync_allow_http_lan) }\n            body.addView(koAllowHttpLan)\n', '')
s = s.replace('val found = LocalScanner.scan(this, uri) { cancelled.get() || Thread.currentThread().isInterrupted }', 'val found = MrproImporter.importBackup(this, uri) { cancelled.get() || Thread.currentThread().isInterrupted }')
s = s.replace('allowHttpLan = koAllowHttpLan.isChecked,', 'allowHttpLan = false,')
main.write_text(s)

# Force KOSync to HTTPS. Filename-ID mode can use the original filename from the backup without materializing the ebook.
ko = root/'app/src/main/java/dev/moonmigration/app/KoSyncClient.kt'
s = ko.read_text()
s = s.replace('    private fun validateBaseUrl(raw: String, allowLan: Boolean, allowHttpLan: Boolean): URI {', '    internal fun validateBaseUrl(raw: String, allowLan: Boolean, allowHttpLan: Boolean): URI {')
s = s.replace('require(scheme == "https" || scheme == "http") { "HTTPS required" }', 'require(scheme == "https") { "HTTPS required" }')
s = re.sub(r'\n        if \(scheme == "http"\) \{.*?\n        \}\n        val normalized =', '\n        val normalized =', s, flags=re.S)
s = s.replace('''                val percent = book.moonPosition?.percent\n                val ebook = book.ebookUri\n                if (percent == null || ebook == null) {\n                    skipped++\n                    continue\n                }\n                val document = when (config.documentIdMode) {\n                    DocumentIdMode.PARTIAL_MD5 -> {\n                        val hash = partialMd5(context, ebook)''', '''                val percent = book.moonPosition?.percent\n                if (percent == null) {\n                    skipped++\n                    continue\n                }\n                val document = when (config.documentIdMode) {\n                    DocumentIdMode.PARTIAL_MD5 -> {\n                        val ebook = book.ebookUri\n                        if (ebook == null) { skipped++; continue }\n                        val hash = partialMd5(context, ebook)''')
s = s.replace('DocumentIdMode.FILE_NAME -> md5Hex((book.ebookDisplayName ?: book.displayName).toByteArray(Charsets.UTF_8))', 'DocumentIdMode.FILE_NAME -> md5Hex((book.ebookDisplayName ?: book.sourceFilename?.substringAfterLast(\'/\') ?: book.displayName).toByteArray(Charsets.UTF_8))')
s = s.replace('User-Agent", "Moon-Migration/1.1"', 'User-Agent", "Moon-Migration/1.2"')
ko.write_text(s)

# Preserve database annotations in the migration report so no recovered annotation metadata is silently discarded.
exp = root/'app/src/main/java/dev/moonmigration/app/Exporter.kt'
s = exp.read_text()
s = s.replace('''                    .put("epubCfi", book.cfi ?: JSONObject.NULL)\n                report.put(json)''', '''                    .put("epubCfi", book.cfi ?: JSONObject.NULL)\n                    .put("author", book.author ?: JSONObject.NULL)\n                    .put("sourceFilename", book.sourceFilename ?: JSONObject.NULL)\n                    .put("ebookIncludedInBackup", book.includedInBackup)\n                if (book.annotations.isNotEmpty()) {\n                    val annotations = JSONArray()\n                    for (a in book.annotations) {\n                        annotations.put(JSONObject()\n                            .put("timestamp", a.timestamp ?: JSONObject.NULL)\n                            .put("chapter", a.chapter ?: JSONObject.NULL)\n                            .put("splitIndex", a.splitIndex ?: JSONObject.NULL)\n                            .put("position", a.position ?: JSONObject.NULL)\n                            .put("length", a.length ?: JSONObject.NULL)\n                            .put("note", a.note ?: JSONObject.NULL)\n                            .put("original", a.original ?: JSONObject.NULL)\n                            .put("bookmark", a.bookmark ?: JSONObject.NULL))\n                    }\n                    json.put("annotations", annotations)\n                }\n                report.put(json)''')
exp.write_text(s)

# Ensure user-facing HTTPS text remains accurate even though older resource key may still exist.
for sub in ['values', 'values-de']:
    p = root/f'app/src/main/res/{sub}/strings.xml'
    x = p.read_text()
    if sub == 'values-de':
        x = re.sub(r'<string name="kosync_allow_http_lan">.*?</string>', '<string name="kosync_allow_http_lan">HTTP wird aus Sicherheitsgründen nicht unterstützt</string>', x)
    else:
        x = re.sub(r'<string name="kosync_allow_http_lan">.*?</string>', '<string name="kosync_allow_http_lan">HTTP is not supported for security reasons</string>', x)
    p.write_text(x)
