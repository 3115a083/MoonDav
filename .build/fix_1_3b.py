from pathlib import Path
import re
root=Path('/tmp/src/MoonMigration')

# Compile fixes for 1.3 importer.
p=root/'app/src/main/java/dev/moonmigration/app/Models.kt'; s=p.read_text(); s=s.replace('    val embeddedTag: String? = null,','    var embeddedTag: String? = null,'); p.write_text(s)
p=root/'app/src/main/java/dev/moonmigration/app/MrproImporter.kt'; s=p.read_text(); s=s.replace('''                coverTags[tag]?.let { indexes ->
                    val bytes=try{readLimited(zip,MAX_COVER_BYTES)}catch(_:IllegalArgumentException){ByteArray(0)}
                    if(bytes.isNotEmpty()) for(i in indexes) { val f=File(cache,"cover-$i.img"); f.writeBytes(bytes); books[i].coverCachePath=f.path }
                    continue
                }
                bookTags[tag]?.let { indexes ->''','''                val coverIndexes = coverTags[tag]
                if (coverIndexes != null) {
                    val bytes=try{readLimited(zip,MAX_COVER_BYTES)}catch(_:IllegalArgumentException){ByteArray(0)}
                    if(bytes.isNotEmpty()) for(i in coverIndexes) { val f=File(cache,"cover-$i.img"); f.writeBytes(bytes); books[i].coverCachePath=f.path }
                    continue
                }
                bookTags[tag]?.let { indexes ->'''); p.write_text(s)

# Selectable/filterable book list and selected-only export/KOSync.
p=root/'app/src/main/java/dev/moonmigration/app/MainActivity.kt'; s=p.read_text()
s=s.replace('import android.widget.ImageView','import android.widget.ImageView\nimport android.widget.ArrayAdapter\nimport android.widget.Spinner\nimport android.widget.AdapterView')
s=s.replace('    private var books: MutableList<BookEntry> = mutableListOf()','    private var books: MutableList<BookEntry> = mutableListOf()\n    private val selectedBookKeys = linkedSetOf<String>()\n    private lateinit var filterSpinner: Spinner\n    private lateinit var selectionText: TextView')
# Add filters/selection controls at top of books card.
s=s.replace('''        root.addView(card(R.string.books_title, null) { body ->
            booksEmpty = secondaryText(getString(R.string.books_empty))''','''        root.addView(card(R.string.books_title, R.string.books_desc) { body ->
            filterSpinner = Spinner(this).apply {
                adapter = ArrayAdapter(this@MainActivity, android.R.layout.simple_spinner_dropdown_item, listOf(
                    getString(R.string.filter_progress), getString(R.string.filter_all), getString(R.string.filter_no_progress), getString(R.string.filter_embedded)
                ))
                onItemSelectedListener = object : AdapterView.OnItemSelectedListener {
                    override fun onItemSelected(parent: AdapterView<*>?, view: View?, position: Int, id: Long) { renderBooks() }
                    override fun onNothingSelected(parent: AdapterView<*>?) {}
                }
            }
            body.addView(filterSpinner)
            val selectionRow = LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL }
            selectionRow.addView(button(R.string.select_visible) { visibleBooks().filter { hasProgress(it) }.forEach { selectedBookKeys.add(it.key) }; renderBooks() }, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
            selectionRow.addView(button(R.string.clear_selection) { selectedBookKeys.clear(); renderBooks() }, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
            body.addView(selectionRow)
            selectionText = secondaryText("")
            body.addView(selectionText)
            booksEmpty = secondaryText(getString(R.string.books_empty))''')
# Initialize selection after import.
s=s.replace('books = found.toMutableList()\n                    renderBooks()', 'books = found.toMutableList()\n                    selectedBookKeys.clear()\n                    books.filter { hasProgress(it) }.forEach { selectedBookKeys.add(it.key) }\n                    renderBooks()')
# Replace renderBooks with selectable rich cards, preserving updateImportProgress marker.
pat=re.compile(r'    private fun renderBooks\(\) \{.*?\n    \}\n\n    private fun updateImportProgress', re.S)
replacement=r'''    private fun hasProgress(book: BookEntry): Boolean = book.moonPosition?.percent != null || book.cfi != null

    private fun visibleBooks(): List<BookEntry> {
        if (!::filterSpinner.isInitialized) return books
        return when (filterSpinner.selectedItemPosition) {
            0 -> books.filter(::hasProgress)
            2 -> books.filter { !hasProgress(it) }
            3 -> books.filter { it.includedInBackup }
            else -> books
        }
    }

    private fun selectedBooks(): List<BookEntry> = books.filter { selectedBookKeys.contains(it.key) }

    private fun renderBooks() {
        booksContainer.removeAllViews()
        val shown = visibleBooks()
        booksEmpty.visibility = if (shown.isEmpty()) View.VISIBLE else View.GONE
        if (::selectionText.isInitialized) selectionText.text = getString(R.string.selection_count, selectedBookKeys.size, books.size)
        shown.forEach { book ->
            val index = books.indexOf(book)
            val card = MaterialCardView(this).apply { radius=dp(14).toFloat(); cardElevation=dp(1).toFloat(); setContentPadding(dp(12),dp(12),dp(12),dp(12)) }
            val outer = LinearLayout(this).apply { orientation=LinearLayout.HORIZONTAL; gravity=Gravity.TOP }
            val cover = ImageView(this).apply {
                scaleType=ImageView.ScaleType.CENTER_CROP
                setBackgroundColor(0x11000000)
                setImageResource(android.R.drawable.ic_menu_gallery)
                book.coverCachePath?.let { BitmapFactory.decodeFile(it) }?.let { setImageBitmap(it) }
            }
            outer.addView(cover, LinearLayout.LayoutParams(dp(82),dp(116)).apply { marginEnd=dp(12) })
            val details=LinearLayout(this).apply { orientation=LinearLayout.VERTICAL }
            val selector=CheckBox(this).apply {
                text=book.displayName; textSize=17f; setTypeface(typeface,Typeface.BOLD); isChecked=selectedBookKeys.contains(book.key)
                isEnabled=hasProgress(book)
                setOnCheckedChangeListener { _, checked -> if(checked) selectedBookKeys.add(book.key) else selectedBookKeys.remove(book.key); selectionText.text=getString(R.string.selection_count,selectedBookKeys.size,books.size) }
            }
            details.addView(selector)
            book.author?.takeIf{it.isNotBlank()}?.let { details.addView(secondaryText(it)) }
            val progressLabel=book.moonPosition?.percent?.let { getString(R.string.reading_progress_fmt,it) } ?: getString(R.string.reading_progress_unknown)
            details.addView(TextView(this).apply { text=progressLabel; textSize=15f; setPadding(0,dp(6),0,0); setTypeface(typeface,Typeface.BOLD) })
            val q=when(book.quality){PositionQuality.EXACT->getString(R.string.exact);PositionQuality.PERCENTAGE->getString(R.string.percentage);PositionQuality.UNRESOLVED->getString(R.string.unresolved)}
            details.addView(secondaryText(q))
            book.isbn?.let { details.addView(secondaryText(getString(R.string.isbn_fmt,it))) }
            val format=book.sourceFilename?.substringAfterLast('.')?.uppercase(Locale.getDefault()); if(format!=null) details.addView(secondaryText(getString(R.string.format_fmt,format)))
            details.addView(secondaryText(if(book.includedInBackup) getString(R.string.book_file_in_backup) else getString(R.string.book_file_missing)))
            details.addView(secondaryText(getString(R.string.annotations_fmt,book.annotations.size)))
            details.addView(button(if(book.ebookUri==null) R.string.assign_ebook else R.string.ebook_assigned) { pendingBookIndex=index; chooseEbook.launch(arrayOf("application/epub+zip","application/pdf","application/octet-stream")) })
            outer.addView(details,LinearLayout.LayoutParams(0,ViewGroup.LayoutParams.WRAP_CONTENT,1f)); card.addView(outer)
            booksContainer.addView(card,LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT,ViewGroup.LayoutParams.WRAP_CONTENT).apply{bottomMargin=dp(10)})
        }
    }

    private fun updateImportProgress'''
s,n=pat.subn(replacement,s,count=1)
if n!=1: raise SystemExit('renderBooks patch failed')
# Export selected only.
s=s.replace('if (books.isEmpty()) { showError(getString(R.string.error_no_books)); return }','if (selectedBooks().isEmpty()) { showError(getString(R.string.error_no_selection)); return }',1)
s=s.replace('KoSyncClient.upload(this, config, books,','KoSyncClient.upload(this, config, selectedBooks(),')
s=s.replace('''    private fun confirmExport() {
        if (destinationUri == null) { showError(getString(R.string.error_destination)); return }
        AlertDialog.Builder(this)
            .setTitle(R.string.export_title)
            .setMessage(R.string.export_desc)''','''    private fun confirmExport() {
        if (destinationUri == null) { showError(getString(R.string.error_destination)); return }
        val selected = selectedBooks()
        if (selected.isEmpty()) { showError(getString(R.string.error_no_selection)); return }
        AlertDialog.Builder(this)
            .setTitle(R.string.export_title)
            .setMessage(getString(R.string.export_selected_confirm, selected.size))''')
s=s.replace('Exporter.export(this, target, books,','Exporter.export(this, target, selectedBooks(),')
s=s.replace('exportButton = button(R.string.export) { confirmExport() }','exportButton = button(R.string.export_selected) { confirmExport() }')
p.write_text(s)

# Add strings and adaptive icon without white PNG border.
for folder,de in [('values',False),('values-de',True)]:
    p=root/f'app/src/main/res/{folder}/strings.xml'; s=p.read_text()
    vals = {
        'books_desc': ('Select books individually and filter the list. Only selected books with reading progress are exported.','Bücher einzeln auswählen und die Liste filtern. Exportiert werden nur ausgewählte Bücher mit Lesefortschritt.'),
        'filter_progress': ('With reading progress','Mit Lesefortschritt'),
        'filter_all': ('All books','Alle Bücher'),
        'filter_no_progress': ('Without reading progress','Ohne Lesefortschritt'),
        'filter_embedded': ('Book file included','Buchdatei enthalten'),
        'select_visible': ('Select visible','Sichtbare auswählen'),
        'clear_selection': ('Clear selection','Auswahl leeren'),
        'selection_count': ('%1$d of %2$d selected','%1$d von %2$d ausgewählt'),
        'annotations_fmt': ('Notes/highlights: %1$d','Notizen/Markierungen: %1$d'),
        'error_no_selection': ('Select at least one book with reading progress.','Wähle mindestens ein Buch mit Lesefortschritt aus.'),
        'export_selected': ('Export selected books','Ausgewählte Bücher exportieren'),
        'export_selected_confirm': ('Export migration data for %1$d selected books?','Migrationsdaten für %1$d ausgewählte Bücher exportieren?')
    }
    for key,(en,ger) in vals.items():
        value=ger if de else en
        if re.search(fr'<string name="{key}">.*?</string>',s): s=re.sub(fr'<string name="{key}">.*?</string>',f'<string name="{key}">{value}</string>',s)
        else: s=s.replace('</resources>',f'    <string name="{key}">{value}</string>\n</resources>')
    p.write_text(s)

(root/'app/src/main/res/drawable/ic_launcher_foreground.xml').write_text('''<vector xmlns:android="http://schemas.android.com/apk/res/android" android:width="108dp" android:height="108dp" android:viewportWidth="108" android:viewportHeight="108">\n    <path android:fillColor="@color/launcher_fg" android:pathData="M58,19c-13,4 -22,16 -22,30c0,17 14,31 31,31c5,0 10,-1 14,-4c-6,10 -17,17 -30,17c-20,0 -36,-16 -36,-36c0,-18 13,-33 31,-36c4,-1 8,-1 12,-2z"/>\n    <path android:fillColor="@color/launcher_fg" android:pathData="M23,61h62v7H23zM28,68h52l-7,20H35z"/>\n    <path android:fillColor="@color/launcher_bg" android:pathData="M36,74h36l-3,8H39z"/>\n</vector>\n''')
(root/'app/src/main/res/values/colors.xml').write_text('''<resources>\n    <color name="launcher_bg">#263238</color>\n    <color name="launcher_fg">#FFFFFF</color>\n</resources>\n''')
p=root/'app/src/main/AndroidManifest.xml'; s=p.read_text().replace('android:icon="@drawable/moon_migration_icon"','android:icon="@mipmap/ic_launcher"').replace('android:roundIcon="@drawable/moon_migration_icon"','android:roundIcon="@mipmap/ic_launcher_round"'); p.write_text(s)
