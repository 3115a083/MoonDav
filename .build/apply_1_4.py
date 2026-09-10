from pathlib import Path
import re
root=Path('/tmp/src/MoonMigration')

# Version 1.4.0
p=root/'app/build.gradle.kts'; s=p.read_text(); s=s.replace('versionCode = 5','versionCode = 6').replace('versionName = "1.3.0"','versionName = "1.4.0"'); p.write_text(s)

# Improve EPUB resolver: Moon+ chapter references can differ by one / use section index.
p=root/'app/src/main/java/dev/moonmigration/app/EpubPositionResolver.kt'; s=p.read_text()
old='''        return try {
            val pkg = readPackage(open) ?: return fallback(position)
            if (chapter !in pkg.spine.indices) return fallback(position)
            val id = pkg.spine[chapter]
            val href = pkg.hrefs[id] ?: return fallback(position)
            val targetPath = resolvePath(pkg.opfPath, href)
            val xhtml = readZipEntry(open, targetPath) ?: return fallback(position)
            val contentCfi = locateTextCfi(xhtml, offset) ?: return fallback(position)
            val packageStep = (chapter + 1) * 2
            Result("epubcfi(/6/$packageStep!$contentCfi)", PositionQuality.EXACT)
        } catch (_: Throwable) {
            fallback(position)
        }
'''
new='''        return try {
            val pkg = readPackage(open) ?: return fallback(position)
            val candidates = linkedSetOf<Int>().apply {
                add(chapter)
                if (chapter > 0) add(chapter - 1)
                position.section?.let { add(it); if (it > 0) add(it - 1) }
            }
            for (candidate in candidates) {
                if (candidate !in pkg.spine.indices) continue
                val id = pkg.spine[candidate]
                val href = pkg.hrefs[id] ?: continue
                val targetPath = resolvePath(pkg.opfPath, href)
                val xhtml = readZipEntry(open, targetPath) ?: continue
                val contentCfi = locateTextCfi(xhtml, offset) ?: continue
                val packageStep = (candidate + 1) * 2
                return Result("epubcfi(/6/$packageStep!$contentCfi)", PositionQuality.EXACT)
            }
            fallback(position)
        } catch (_: Throwable) {
            fallback(position)
        }
'''
if old not in s: raise SystemExit('resolver block not found')
s=s.replace(old,new); p.write_text(s)

# Robust EPUB metadata and cover detection, regardless of XML attribute order.
p=root/'app/src/main/java/dev/moonmigration/app/MrproImporter.kt'; s=p.read_text()
old_start=s.index('    private fun readEpubMetadata(file: File): Pair<String?,ByteArray?> {')
old_end=s.index('\n    private fun parsePositions', old_start)
new_func=r'''    private fun readEpubMetadata(file: File): Pair<String?,ByteArray?> {
        return try {
            ZipFile(file).use { z ->
                val container=z.getEntry("META-INF/container.xml") ?: return null to null
                val c=z.getInputStream(container).bufferedReader().use{it.readText()}
                val opfPath=Regex("full-path\\s*=\\s*[\"']([^\"']+)",RegexOption.IGNORE_CASE).find(c)?.groupValues?.get(1) ?: return null to null
                val opfEntry=z.getEntry(opfPath) ?: return null to null
                val opf=z.getInputStream(opfEntry).bufferedReader().use{it.readText()}
                val isbn=findIsbn(opf)
                fun attrs(tag:String):Map<String,String> = Regex("([A-Za-z0-9:_-]+)\\s*=\\s*[\"']([^\"']*)[\"']").findAll(tag).associate { it.groupValues[1].lowercase(Locale.ROOT) to it.groupValues[2] }
                val itemTags=Regex("<item\\b[^>]*>",RegexOption.IGNORE_CASE).findAll(opf).map { attrs(it.value) }.toList()
                val metaTags=Regex("<meta\\b[^>]*>",RegexOption.IGNORE_CASE).findAll(opf).map { attrs(it.value) }.toList()
                val coverId=metaTags.firstOrNull { it["name"]?.equals("cover",true)==true }?.get("content")
                val coverItem=itemTags.firstOrNull { m ->
                    (coverId!=null && m["id"]==coverId) || m["properties"].orEmpty().split(Regex("\\s+")).any { it.equals("cover-image",true) }
                } ?: itemTags.firstOrNull { m ->
                    val h=m["href"].orEmpty().lowercase(Locale.ROOT)
                    (h.contains("cover") || m["id"].orEmpty().lowercase(Locale.ROOT).contains("cover")) &&
                        (h.endsWith(".jpg") || h.endsWith(".jpeg") || h.endsWith(".png") || h.endsWith(".webp"))
                }
                val href=coverItem?.get("href")
                val base=opfPath.substringBeforeLast('/',"")
                val coverPath=href?.let { java.net.URLDecoder.decode(if(base.isBlank()) it else "$base/$it", "UTF-8").substringBefore('#') }
                val cover=coverPath?.let { z.getEntry(it) }?.takeIf { it.size in 1..MAX_COVER_BYTES }?.let { z.getInputStream(it).use { input -> readLimited(input,MAX_COVER_BYTES) } }
                isbn to cover
            }
        } catch(_:Throwable) { null to null }
    }
'''
s=s[:old_start]+new_func+s[old_end:]; p.write_text(s)

# Export actual selected book files, either assigned directly or embedded in .mrpro.
p=root/'app/src/main/java/dev/moonmigration/app/Exporter.kt'
p.write_text(r'''package dev.moonmigration.app

import android.content.Context
import android.net.Uri
import androidx.documentfile.provider.DocumentFile
import org.json.JSONArray
import org.json.JSONObject
import java.util.Locale
import java.util.zip.ZipInputStream

object Exporter {
    data class Progress(val done: Int, val total: Int, val phase: String)

    fun export(
        context: Context,
        targetTree: Uri,
        sourceBackup: Uri?,
        books: List<BookEntry>,
        isCancelled: () -> Boolean,
        onProgress: (Progress) -> Unit
    ) {
        val root = DocumentFile.fromTreeUri(context, targetTree) ?: error("Destination unavailable")
        require(root.canWrite()) { "Destination is not writable" }
        val stagingName = ".moon-migration-staging"
        root.findFile(stagingName)?.delete()
        val staging = root.createDirectory(stagingName) ?: error("Cannot create staging directory")
        val report = JSONArray()
        var completed = 0

        try {
            for (book in books) {
                if (isCancelled()) throw InterruptedException("cancelled")
                val direct = book.ebookUri
                if (direct != null) {
                    val ext = extensionFor(book)
                    val out = staging.createFile(mimeFor(ext), uniqueName(staging, safeFileName(book.displayName), ext)) ?: error("Cannot create ebook export")
                    context.contentResolver.openInputStream(direct)!!.use { input -> context.contentResolver.openOutputStream(out.uri,"w")!!.use { output -> input.copyTo(output,256*1024) } }
                }
            }

            val embedded = books.filter { it.ebookUri == null && it.includedInBackup && !it.embeddedTag.isNullOrBlank() }
            if (embedded.isNotEmpty()) {
                val source = sourceBackup ?: error("Original Moon+ backup is required for embedded ebook export")
                val wanted = embedded.groupBy { it.embeddedTag!! }
                val input = context.contentResolver.openInputStream(source) ?: error("Backup unavailable")
                ZipInputStream(input.buffered(256*1024)).use { zip ->
                    var count=0
                    while(true) {
                        if(isCancelled()) throw InterruptedException("cancelled")
                        val e=zip.nextEntry ?: break
                        count++; require(count<=200_000){"Too many backup entries"}
                        if(e.isDirectory) continue
                        val tag=e.name.replace('\\','/').substringAfterLast('/')
                        val matches=wanted[tag] ?: continue
                        val bytesTemp = if(matches.size>1) java.io.File.createTempFile("moon-export-", ".bin", context.cacheDir) else null
                        try {
                            if(bytesTemp!=null) {
                                bytesTemp.outputStream().use { out -> zip.copyTo(out,256*1024) }
                                for(book in matches) copyFileToStaging(context, staging, book, bytesTemp.inputStream())
                            } else {
                                copyFileToStaging(context, staging, matches.first(), zip)
                            }
                        } finally { bytesTemp?.delete() }
                    }
                }
            }

            for (book in books) {
                if (isCancelled()) throw InterruptedException("cancelled")
                onProgress(Progress(completed, books.size, "export"))
                if (book.anUri != null) {
                    val compressed = context.contentResolver.openInputStream(book.anUri)?.use { it.readBytes() } ?: error("Annotation sidecar unavailable")
                    val inflated = MoonParser.inflateAnnotation(compressed)
                    val outName = uniqueName(staging, safeFileName(book.displayName), ".mrexpt")
                    val out = staging.createFile("application/octet-stream", outName) ?: error("Cannot create export")
                    context.contentResolver.openOutputStream(out.uri, "w")!!.use { it.write(inflated) }
                }
                val json = JSONObject()
                    .put("book", book.displayName)
                    .put("status", book.quality.name.lowercase(Locale.ROOT))
                    .put("moonPosition", book.moonPosition?.raw ?: JSONObject.NULL)
                    .put("percent", book.moonPosition?.percent ?: JSONObject.NULL)
                    .put("epubCfi", book.cfi ?: JSONObject.NULL)
                    .put("author", book.author ?: JSONObject.NULL)
                    .put("isbn", book.isbn ?: JSONObject.NULL)
                    .put("sourceFilename", book.sourceFilename ?: JSONObject.NULL)
                    .put("ebookIncludedInBackup", book.includedInBackup)
                if (book.annotations.isNotEmpty()) {
                    val annotations = JSONArray()
                    for (a in book.annotations) annotations.put(JSONObject().put("timestamp",a.timestamp?:JSONObject.NULL).put("chapter",a.chapter?:JSONObject.NULL).put("splitIndex",a.splitIndex?:JSONObject.NULL).put("position",a.position?:JSONObject.NULL).put("length",a.length?:JSONObject.NULL).put("note",a.note?:JSONObject.NULL).put("original",a.original?:JSONObject.NULL).put("bookmark",a.bookmark?:JSONObject.NULL))
                    json.put("annotations", annotations)
                }
                report.put(json); completed++
            }
            val reportFile = staging.createFile("application/json", "moon-migration-report.json") ?: error("Cannot create report")
            context.contentResolver.openOutputStream(reportFile.uri, "w")!!.bufferedWriter().use { it.write(report.toString(2)) }

            for (f in staging.listFiles()) {
                if (isCancelled()) throw InterruptedException("cancelled")
                val name=f.name ?: continue
                root.findFile(name)?.delete()
                val dst=root.createFile(f.type ?: "application/octet-stream", name) ?: error("Cannot finalize export")
                context.contentResolver.openInputStream(f.uri)!!.use { input -> context.contentResolver.openOutputStream(dst.uri,"w")!!.use { output -> input.copyTo(output,256*1024) } }
            }
            staging.delete(); onProgress(Progress(books.size,books.size,"done"))
        } catch(e:Throwable) { staging.delete(); throw e }
    }

    private fun copyFileToStaging(context:Context, staging:DocumentFile, book:BookEntry, input:java.io.InputStream) {
        val ext=extensionFor(book); val out=staging.createFile(mimeFor(ext), uniqueName(staging,safeFileName(book.displayName),ext)) ?: error("Cannot create ebook export")
        context.contentResolver.openOutputStream(out.uri,"w")!!.use { output -> input.copyTo(output,256*1024) }
    }
    private fun extensionFor(book:BookEntry):String { val raw=(book.ebookDisplayName ?: book.sourceFilename ?: "").substringAfterLast('.',"").lowercase(Locale.ROOT); return if(raw.matches(Regex("[a-z0-9]{1,8}"))) ".$raw" else ".bin" }
    private fun mimeFor(ext:String)=when(ext){".epub"->"application/epub+zip";".pdf"->"application/pdf";".txt"->"text/plain";else->"application/octet-stream"}
    private fun uniqueName(dir:DocumentFile, base:String, ext:String):String { var n="$base$ext"; var i=2; while(dir.findFile(n)!=null){n="$base ($i)$ext";i++}; return n }
    internal fun safeFileName(value: String): String { val cleaned=value.replace(Regex("[\\x00-\\x1F\\x7F/\\\\:*?\"<>|]"),"_").trim().take(100); return if(cleaned.isBlank())"Unresolved Book" else cleaned }
}
''')

# Main UI: 0% is no progress, deferred destination permission, balanced buttons, article-aware sorting and alphabet quick navigation.
p=root/'app/src/main/java/dev/moonmigration/app/MainActivity.kt'; s=p.read_text()
s=s.replace('    private lateinit var root: LinearLayout', '    private lateinit var root: LinearLayout\n    private lateinit var mainScroll: ScrollView')
s=s.replace('    private val selectedBookKeys = linkedSetOf<String>()', '    private val selectedBookKeys = linkedSetOf<String>()\n    private var pendingExportAfterDestination = false\n    private val alphabetAnchors = linkedMapOf<Char, View>()')
s=s.replace('''            destinationUri = uri
            destinationStatus.setText(R.string.destination_selected)
''','''            destinationUri = uri
            destinationStatus.setText(R.string.destination_selected)
            if (pendingExportAfterDestination) {
                pendingExportAfterDestination = false
                confirmExport()
            }
''')
s=s.replace('''        val scroll = ScrollView(this).apply {
            isFillViewport = true
            clipToPadding = false
        }
''','''        mainScroll = ScrollView(this).apply {
            isFillViewport = true
            clipToPadding = false
            isVerticalScrollBarEnabled = true
            scrollbarStyle = View.SCROLLBARS_INSIDE_INSET
        }
''').replace('scroll.addView(root,', 'mainScroll.addView(root,').replace('setContentView(scroll)', 'setContentView(mainScroll)')
s=s.replace('''            selectionRow.addView(button(R.string.select_visible) { visibleBooks().filter { hasProgress(it) }.forEach { selectedBookKeys.add(it.key) }; renderBooks() }, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
            selectionRow.addView(button(R.string.clear_selection) { selectedBookKeys.clear(); renderBooks() }, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
''','''            val selectVisibleButton = button(R.string.select_visible) { visibleBooks().filter { hasProgress(it) }.forEach { selectedBookKeys.add(it.key) }; renderBooks() }
            val clearSelectionButton = button(R.string.clear_selection) { selectedBookKeys.clear(); renderBooks() }
            selectionRow.addView(selectVisibleButton, LinearLayout.LayoutParams(0, dp(56), 1f).apply { marginEnd=dp(4) })
            selectionRow.addView(clearSelectionButton, LinearLayout.LayoutParams(0, dp(56), 1f).apply { marginStart=dp(4) })
''')
s=s.replace('''            booksContainer = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
            body.addView(booksContainer)
''','''            val jumpRow=LinearLayout(this).apply { orientation=LinearLayout.HORIZONTAL }
            jumpRow.addView(button(R.string.jump_top) { mainScroll.smoothScrollTo(0,0) }, LinearLayout.LayoutParams(0,dp(48),1f).apply{marginEnd=dp(4)})
            jumpRow.addView(button(R.string.jump_bottom) { mainScroll.post { mainScroll.smoothScrollTo(0, root.height) } }, LinearLayout.LayoutParams(0,dp(48),1f).apply{marginStart=dp(4)})
            body.addView(jumpRow)
            val listRow=LinearLayout(this).apply { orientation=LinearLayout.HORIZONTAL; gravity=Gravity.TOP }
            booksContainer = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
            listRow.addView(booksContainer, LinearLayout.LayoutParams(0,ViewGroup.LayoutParams.WRAP_CONTENT,1f))
            val alphabet=LinearLayout(this).apply { orientation=LinearLayout.VERTICAL; gravity=Gravity.CENTER_HORIZONTAL; setPadding(dp(4),0,0,0) }
            for(letter in ('A'..'Z')) alphabet.addView(TextView(this).apply { text=letter.toString(); textSize=12f; gravity=Gravity.CENTER; minWidth=dp(30); minHeight=dp(24); setOnClickListener { alphabetAnchors[letter]?.requestRectangleOnScreen(android.graphics.Rect(0,0,alphabetAnchors[letter]!!.width,alphabetAnchors[letter]!!.height),true) } })
            listRow.addView(alphabet, LinearLayout.LayoutParams(dp(34),ViewGroup.LayoutParams.WRAP_CONTENT))
            body.addView(listRow)
''')
s=s.replace('private fun hasProgress(book: BookEntry): Boolean = book.moonPosition?.percent != null || book.cfi != null', 'private fun hasProgress(book: BookEntry): Boolean = (book.moonPosition?.percent ?: 0.0) > 0.0001 || book.cfi != null')
old='''    private fun visibleBooks(): List<BookEntry> {
        if (!::filterSpinner.isInitialized) return books
        return when (filterSpinner.selectedItemPosition) {
            0 -> books.filter(::hasProgress)
            2 -> books.filter { !hasProgress(it) }
            3 -> books.filter { it.includedInBackup }
            else -> books
        }
    }
'''
new='''    private fun sortTitle(title:String):String {
        val t=title.trim()
        val article=Regex("^(?i)(the|a|an|der|die|das|den|dem|des|ein|eine|einer|eines|einem|einen|le|la|les|un|une|el|los|las)\\s+")
        return t.replaceFirst(article,"").trim().lowercase(Locale.ROOT)
    }
    private fun visibleBooks(): List<BookEntry> {
        val filtered = if (!::filterSpinner.isInitialized) books else when (filterSpinner.selectedItemPosition) {
            0 -> books.filter(::hasProgress)
            2 -> books.filter { !hasProgress(it) }
            3 -> books.filter { it.includedInBackup }
            else -> books
        }
        return filtered.sortedWith(compareBy<BookEntry> { sortTitle(it.displayName) }.thenBy { it.displayName.lowercase(Locale.ROOT) })
    }
'''
if old not in s: raise SystemExit('visibleBooks block missing')
s=s.replace(old,new)
s=s.replace('''        booksContainer.removeAllViews()
        val shown = visibleBooks()
''','''        booksContainer.removeAllViews()
        alphabetAnchors.clear()
        val shown = visibleBooks()
''')
s=s.replace('''            booksContainer.addView(card,LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT,ViewGroup.LayoutParams.WRAP_CONTENT).apply{bottomMargin=dp(10)})
        }
''','''            booksContainer.addView(card,LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT,ViewGroup.LayoutParams.WRAP_CONTENT).apply{bottomMargin=dp(10)})
            val first=sortTitle(book.displayName).firstOrNull()?.uppercaseChar()
            if(first!=null && first in 'A'..'Z' && alphabetAnchors[first]==null) alphabetAnchors[first]=card
        }
''')
s=s.replace('''    private fun confirmExport() {
        if (destinationUri == null) { showError(getString(R.string.error_destination)); return }
''','''    private fun confirmExport() {
        if (destinationUri == null) {
            pendingExportAfterDestination = true
            chooseDestination.launch(null)
            return
        }
''')
s=s.replace('Exporter.export(this, target, selectedBooks(),', 'Exporter.export(this, target, sourceUri, selectedBooks(),')
p.write_text(s)

for folder,de in [('values',False),('values-de',True)]:
    p=root/f'app/src/main/res/{folder}/strings.xml'; x=p.read_text()
    additions = '''
    <string name="jump_top">Nach oben</string>
    <string name="jump_bottom">Nach unten</string>
''' if de else '''
    <string name="jump_top">Top</string>
    <string name="jump_bottom">Bottom</string>
'''
    x=x.replace('</resources>',additions+'</resources>'); p.write_text(x)
