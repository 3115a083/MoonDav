from pathlib import Path
import re
root=Path('/tmp/src/MoonMigration')

# Version
p=root/'app/build.gradle.kts'; s=p.read_text(); s=s.replace('versionCode = 6','versionCode = 7').replace('versionName = "1.4.0"','versionName = "1.5.0"'); p.write_text(s)

# Robust Android XML parsing: unsupported JAXP hardening attributes on Android must not disable all EPUB parsing.
p=root/'app/src/main/java/dev/moonmigration/app/EpubPositionResolver.kt'; s=p.read_text()
old='''    private fun parseXml(bytes: ByteArray) = try {
        val f = DocumentBuilderFactory.newInstance().apply {
            isNamespaceAware = false
            setFeature("http://apache.org/xml/features/disallow-doctype-decl", true)
            setFeature("http://xml.org/sax/features/external-general-entities", false)
            setFeature("http://xml.org/sax/features/external-parameter-entities", false)
            setAttribute("http://javax.xml.XMLConstants/property/accessExternalDTD", "")
            setAttribute("http://javax.xml.XMLConstants/property/accessExternalSchema", "")
        }
        f.newDocumentBuilder().parse(ByteArrayInputStream(bytes))
    } catch (_: Throwable) { null }
'''
new='''    private fun parseXml(bytes: ByteArray) = try {
        val probe = bytes.toString(Charsets.UTF_8).lowercase(java.util.Locale.ROOT)
        if ("<!doctype" in probe || "<!entity" in probe) return null
        val f = DocumentBuilderFactory.newInstance().apply {
            isNamespaceAware = false
            try { setFeature("http://apache.org/xml/features/disallow-doctype-decl", true) } catch (_: Throwable) { }
            try { setFeature("http://xml.org/sax/features/external-general-entities", false) } catch (_: Throwable) { }
            try { setFeature("http://xml.org/sax/features/external-parameter-entities", false) } catch (_: Throwable) { }
            try { setFeature("http://apache.org/xml/features/nonvalidating/load-external-dtd", false) } catch (_: Throwable) { }
            try { setAttribute("http://javax.xml.XMLConstants/property/accessExternalDTD", "") } catch (_: Throwable) { }
            try { setAttribute("http://javax.xml.XMLConstants/property/accessExternalSchema", "") } catch (_: Throwable) { }
        }
        f.newDocumentBuilder().parse(ByteArrayInputStream(bytes))
    } catch (_: Throwable) { null }
'''
if old not in s: raise SystemExit('parseXml block missing')
s=s.replace(old,new); p.write_text(s)

# Importer: support real Moon+ cover/thumb columns, fast empty-backup exit, safer cover lookup.
p=root/'app/src/main/java/dev/moonmigration/app/MrproImporter.kt'; s=p.read_text()
needle='''            val db = SQLiteDatabase.openDatabase(dbFile.path, null, SQLiteDatabase.OPEN_READONLY)
            val books = db.use { readBooks(it, isCancelled) }.toMutableList()
            val mapper = BackupNameMapper(names)
'''
repl='''            val db = SQLiteDatabase.openDatabase(dbFile.path, null, SQLiteDatabase.OPEN_READONLY)
            val books = db.use { readBooks(it, isCancelled) }.toMutableList()
            if (books.isEmpty()) {
                onProgress(MrproProgress(MrproProgress.Stage.FINALIZING))
                return emptyList()
            }
            val mapper = BackupNameMapper(names)
'''
if needle not in s: raise SystemExit('import fast return anchor missing')
s=s.replace(needle,repl)
start=s.index('        db.rawQuery("SELECT book, filename, lowerFilename, author, description, coverFile, thumbFile FROM books ORDER BY book COLLATE NOCASE",null).use { c ->')
end=s.index('        return out.distinctBy{it.key}', start)
newblock='''        val columns=tableColumns(db,"books")
        fun firstColumn(vararg names:String):String? = names.firstOrNull { columns.contains(it.lowercase(Locale.ROOT)) }
        val coverCol=firstColumn("cover","coverFile","cover_file")
        val thumbCol=firstColumn("thumb","thumbFile","thumb_file")
        val select="SELECT book, filename, lowerFilename, author, description, " +
            (coverCol?.let { "`$it`" } ?: "NULL") + " AS coverCandidate, " +
            (thumbCol?.let { "`$it`" } ?: "NULL") + " AS thumbCandidate FROM books ORDER BY book COLLATE NOCASE"
        db.rawQuery(select,null).use { c ->
            while(c.moveToNext()) {
                checkCancelled(isCancelled)
                val filename=c.str("filename"); val lower=c.str("lowerFilename"); val key=norm(lower?:filename?:c.str("book")?:continue)
                val title=c.str("book")?.trim().takeUnless{it.isNullOrBlank()} ?: filename?.substringAfterLast('/')?.substringBeforeLast('.') ?: "Unresolved book"
                val cover=(c.str("coverCandidate")?.takeIf{it.isNotBlank()} ?: c.str("thumbCandidate")?.takeIf{it.isNotBlank()})?.take(768)
                out += BookEntry(key,title.take(200),null,null,null,quality=PositionQuality.UNRESOLVED,author=c.str("author")?.take(200),sourceFilename=filename?.take(768),annotations=notes[key].orEmpty(),coverSourcePath=cover,isbn=findIsbn(c.str("description")))
            }
        }
'''
s=s[:start]+newblock+s[end:]
s=s.replace('''    private fun tableExists(db:SQLiteDatabase,name:String)=db.rawQuery("SELECT 1 FROM sqlite_master WHERE type='table' AND name=? LIMIT 1",arrayOf(name)).use{it.moveToFirst()}
''','''    private fun tableColumns(db:SQLiteDatabase,name:String):Set<String> = db.rawQuery("PRAGMA table_info(`$name`)",null).use { c ->
        val idx=c.getColumnIndex("name"); val out=linkedSetOf<String>(); while(c.moveToNext()) if(idx>=0 && !c.isNull(idx)) out += c.getString(idx).lowercase(Locale.ROOT); out
    }
    private fun tableExists(db:SQLiteDatabase,name:String)=db.rawQuery("SELECT 1 FROM sqlite_master WHERE type='table' AND name=? LIMIT 1",arrayOf(name)).use{it.moveToFirst()}
''')
p.write_text(s)

# Exporter: verify every requested embedded ebook was actually found before finalizing.
p=root/'app/src/main/java/dev/moonmigration/app/Exporter.kt'; s=p.read_text()
s=s.replace('''                val wanted = embedded.groupBy { it.embeddedTag!! }
                val input = context.contentResolver.openInputStream(source) ?: error("Backup unavailable")
''','''                val wanted = embedded.groupBy { it.embeddedTag!! }
                val foundTags = linkedSetOf<String>()
                val input = context.contentResolver.openInputStream(source) ?: error("Backup unavailable")
''')
s=s.replace('''                        val matches=wanted[tag] ?: continue
                        val bytesTemp = if(matches.size>1)''','''                        val matches=wanted[tag] ?: continue
                        foundTags += tag
                        val bytesTemp = if(matches.size>1)''')
s=s.replace('''                }
            }

            for (book in books) {''','''                }
                val missing = wanted.keys - foundTags
                require(missing.isEmpty()) { "One or more selected embedded ebooks were not found in the backup" }
            }

            for (book in books) {''',1)
p.write_text(s)

# MainActivity UI: language switch, local import cancel, floating scroll controls, equal buttons.
p=root/'app/src/main/java/dev/moonmigration/app/MainActivity.kt'; s=p.read_text()
s=s.replace('import android.widget.TextView','import android.widget.TextView\nimport android.widget.FrameLayout')
s=s.replace('import androidx.appcompat.app.AppCompatActivity','import androidx.appcompat.app.AppCompatActivity\nimport androidx.appcompat.app.AppCompatDelegate\nimport androidx.core.os.LocaleListCompat')
s=s.replace('    private lateinit var importProgressText: TextView','    private lateinit var importProgressText: TextView\n    private lateinit var importCancelButton: MaterialButton')
old='''        mainScroll.addView(root, ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT))
        setContentView(mainScroll)
'''
new='''        mainScroll.addView(root, ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT))
        val host = FrameLayout(this)
        host.addView(mainScroll, FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
        fun floatingArrow(symbol:String, action:()->Unit): MaterialButton = MaterialButton(this).apply {
            text=symbol; textSize=22f; minWidth=0; minHeight=0; cornerRadius=dp(24)
            insetTop=0; insetBottom=0; setPadding(0,0,0,0); setOnClickListener { action() }
            contentDescription=symbol
        }
        val up=floatingArrow("↑") { mainScroll.smoothScrollTo(0,0) }
        val down=floatingArrow("↓") { mainScroll.post { mainScroll.smoothScrollTo(0, root.height) } }
        host.addView(up, FrameLayout.LayoutParams(dp(46),dp(46),Gravity.END or Gravity.TOP).apply { marginEnd=dp(8); topMargin=dp(78) })
        host.addView(down, FrameLayout.LayoutParams(dp(46),dp(46),Gravity.END or Gravity.BOTTOM).apply { marginEnd=dp(8); bottomMargin=dp(78) })
        setContentView(host)
'''
if old not in s: raise SystemExit('host anchor missing')
s=s.replace(old,new)
anchor='''        root.addView(TextView(this).apply {
            text = getString(R.string.privacy_note)
            textSize = 14f
            setPadding(0, 0, 0, dp(12))
        })

'''
lang='''        root.addView(TextView(this).apply {
            text = getString(R.string.privacy_note)
            textSize = 14f
            setPadding(0, 0, 0, dp(12))
        })

        root.addView(card(R.string.language_title, null) { body ->
            val languageSpinner=Spinner(this).apply {
                adapter=ArrayAdapter(this@MainActivity, android.R.layout.simple_spinner_dropdown_item, listOf(getString(R.string.language_de),getString(R.string.language_en)))
                val current=AppCompatDelegate.getApplicationLocales().toLanguageTags()
                setSelection(if(current.startsWith("en")) 1 else 0, false)
                onItemSelectedListener=object:AdapterView.OnItemSelectedListener {
                    override fun onItemSelected(parent:AdapterView<*>?,view:View?,position:Int,id:Long) {
                        val tag=if(position==1) "en" else "de"
                        val existing=AppCompatDelegate.getApplicationLocales().toLanguageTags()
                        if(!existing.startsWith(tag)) AppCompatDelegate.setApplicationLocales(LocaleListCompat.forLanguageTags(tag))
                    }
                    override fun onNothingSelected(parent:AdapterView<*>?) {}
                }
            }
            body.addView(languageSpinner)
        })

'''
if anchor not in s: raise SystemExit('language anchor missing')
s=s.replace(anchor,lang)
s=s.replace('''            importProgress = ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal).apply { max=1000; visibility=View.GONE }
            body.addView(importProgress, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(8)).apply { topMargin=dp(8) })
''','''            importProgress = ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal).apply { max=1000; visibility=View.GONE }
            body.addView(importProgress, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(8)).apply { topMargin=dp(8) })
            importCancelButton = button(R.string.cancel_import) { cancelCurrentOperation() }.apply { visibility=View.GONE }
            body.addView(importCancelButton)
''')
s=re.sub(r'''            val jumpRow=LinearLayout\(this\)\.apply \{ orientation=LinearLayout\.HORIZONTAL \}\n            jumpRow\.addView\(button\(R\.string\.jump_top\).*?\n            body\.addView\(jumpRow\)\n''','',s,flags=re.S)
old='''            val selectVisibleButton = button(R.string.select_visible) { visibleBooks().filter { hasProgress(it) }.forEach { selectedBookKeys.add(it.key) }; renderBooks() }
            val clearSelectionButton = button(R.string.clear_selection) { selectedBookKeys.clear(); renderBooks() }
            selectionRow.addView(selectVisibleButton, LinearLayout.LayoutParams(0, dp(56), 1f).apply { marginEnd=dp(4) })
            selectionRow.addView(clearSelectionButton, LinearLayout.LayoutParams(0, dp(56), 1f).apply { marginStart=dp(4) })
'''
new='''            val selectVisibleButton = button(R.string.select_visible) { visibleBooks().filter { hasProgress(it) }.forEach { selectedBookKeys.add(it.key) }; renderBooks() }.apply { insetTop=0; insetBottom=0; minHeight=0; maxLines=2 }
            val clearSelectionButton = button(R.string.clear_selection) { selectedBookKeys.clear(); renderBooks() }.apply { insetTop=0; insetBottom=0; minHeight=0; maxLines=2 }
            selectionRow.addView(selectVisibleButton, LinearLayout.LayoutParams(0, dp(64), 1f).apply { marginEnd=dp(4) })
            selectionRow.addView(clearSelectionButton, LinearLayout.LayoutParams(0, dp(64), 1f).apply { marginStart=dp(4) })
'''
if old not in s: raise SystemExit('selection buttons missing')
s=s.replace(old,new)
s=s.replace('''        cancelButton.visibility = View.VISIBLE
        exportButton.isEnabled = false
''','''        val isScan = phase == getString(R.string.phase_scan)
        cancelButton.visibility = if (isScan) View.GONE else View.VISIBLE
        importCancelButton.visibility = if (isScan) View.VISIBLE else View.GONE
        exportButton.isEnabled = false
''')
s=s.replace('''        cancelButton.visibility = View.GONE
        exportButton.isEnabled = true
''','''        cancelButton.visibility = View.GONE
        importCancelButton.visibility = View.GONE
        exportButton.isEnabled = true
''')
s=s.replace('''            } catch (_: InterruptedException) {
                runOnUiThread { finishOperation(getString(R.string.phase_cancelled)) }
            } catch (_: Throwable) {
                runOnUiThread {
                    finishOperation(getString(R.string.phase_idle))
                    showError(getString(R.string.error_scan))
''','''            } catch (_: InterruptedException) {
                runOnUiThread { importProgressText.setText(R.string.import_cancelled); finishOperation(getString(R.string.phase_cancelled)) }
            } catch (_: Throwable) {
                runOnUiThread {
                    finishOperation(getString(R.string.phase_idle))
                    showError(getString(R.string.error_scan))
''',1)
p.write_text(s)

for folder,de in [('values',False),('values-de',True)]:
    p=root/f'app/src/main/res/{folder}/strings.xml'; s=p.read_text()
    vals={
      'language_title':('Language','Sprache'),
      'language_de':('Deutsch','Deutsch'),
      'language_en':('English','English'),
      'cancel_import':('Cancel import','Einlesen abbrechen'),
      'import_cancelled':('Import cancelled.','Einlesen abgebrochen.')
    }
    for key,(en,ger) in vals.items():
        v=ger if de else en
        if re.search(fr'<string name="{key}">.*?</string>',s): s=re.sub(fr'<string name="{key}">.*?</string>',f'<string name="{key}">{v}</string>',s)
        else: s=s.replace('</resources>',f'    <string name="{key}">{v}</string>\n</resources>')
    p.write_text(s)

# Use original moon artwork from moon_icon.b64 and remove only border-connected white background.
icon_script=root/'make_icon_15.py'
icon_script.write_text(r'''from pathlib import Path
import base64
from PIL import Image
from collections import deque
root=Path('/tmp/src/MoonMigration')
raw=base64.b64decode(Path('.build/moon_icon.b64').read_text())
src=Path('/tmp/original-moon.png'); src.write_bytes(raw)
im=Image.open(src).convert('RGBA')
px=im.load(); w,h=im.size; q=deque(); seen=set()
for x in range(w): q.append((x,0)); q.append((x,h-1))
for y in range(h): q.append((0,y)); q.append((w-1,y))
while q:
    x,y=q.popleft()
    if (x,y) in seen or x<0 or y<0 or x>=w or y>=h: continue
    seen.add((x,y)); r,g,b,a=px[x,y]
    if not (a>0 and r>238 and g>238 and b>238): continue
    px[x,y]=(r,g,b,0)
    q.extend(((x+1,y),(x-1,y),(x,y+1),(x,y-1)))
bbox=im.getbbox()
if bbox: im=im.crop(bbox)
canvas=Image.new('RGBA',(512,512),(0,0,0,0))
im.thumbnail((430,430),Image.Resampling.LANCZOS)
canvas.alpha_composite(im,((512-im.width)//2,(512-im.height)//2))
canvas.save(root/'app/src/main/res/drawable/moon_migration_icon.png')
fg=canvas.resize((432,432),Image.Resampling.LANCZOS); fg.save(root/'app/src/main/res/drawable/ic_launcher_original_foreground.png')
for name in ('ic_launcher.xml','ic_launcher_round.xml'):
    (root/'app/src/main/res/mipmap-anydpi-v26'/name).write_text('<adaptive-icon xmlns:android="http://schemas.android.com/apk/res/android"><background android:drawable="@color/launcher_bg"/><foreground android:drawable="@drawable/ic_launcher_original_foreground"/></adaptive-icon>')
    (root/'app/src/main/res/mipmap-anydpi'/name).write_text('<bitmap xmlns:android="http://schemas.android.com/apk/res/android" android:src="@drawable/moon_migration_icon" android:gravity="center"/>')
''')
import subprocess
subprocess.run(['python3', str(icon_script)], check=True)
