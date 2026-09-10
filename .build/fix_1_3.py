from pathlib import Path

root = Path('/tmp/src/MoonMigration')

# The importer annotates the resolved tag after reading _names.list.
models = root / 'app/src/main/java/dev/moonmigration/app/Models.kt'
s = models.read_text()
s = s.replace('    val embeddedTag: String? = null,', '    var embeddedTag: String? = null,')
models.write_text(s)

# Avoid continue/return from inline let lambdas; keep ordinary control flow.
imp = root / 'app/src/main/java/dev/moonmigration/app/MrproImporter.kt'
s = imp.read_text()
old = '''                coverTags[tag]?.let { indexes ->
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
'''
new = '''                val coverIndexes = coverTags[tag]
                if (coverIndexes != null) {
                    val bytes=try{readLimited(zip,MAX_COVER_BYTES)}catch(_:IllegalArgumentException){ByteArray(0)}
                    if(bytes.isNotEmpty()) for(i in coverIndexes) { val f=File(cache,"cover-$i.img"); f.writeBytes(bytes); books[i].coverCachePath=f.path }
                    continue
                }
                val bookIndexes = bookTags[tag]
                if (bookIndexes != null && bookIndexes.isNotEmpty()) {
                    val first=bookIndexes.first()
                    val book=books[first]; book.embeddedSize=e.size.takeIf{it>=0}
                    if(book.sourceFilename.orEmpty().lowercase(Locale.ROOT).endsWith(".epub") && e.size<=MAX_EPUB_FOR_EXACT_CFI) {
                        val temp=File(cache,"book-$first.epub")
                        try {
                            copyLimited(zip,temp,MAX_EPUB_FOR_EXACT_CFI)
                            val meta=readEpubMetadata(temp)
                            for(i in bookIndexes) { if(books[i].isbn==null) books[i].isbn=meta.first; if(books[i].coverCachePath==null && meta.second!=null){ val cf=File(cache,"cover-$i.jpg"); cf.writeBytes(meta.second!!); books[i].coverCachePath=cf.path } }
                            book.moonPosition?.let { pos -> EpubPositionResolver.resolve({temp.inputStream()},temp.length(),pos)?.let { r -> for(i in bookIndexes){books[i].cfi=r.cfi; books[i].quality=r.quality} } }
                        } catch(_:Throwable) { } finally { temp.delete() }
                    }
                }
'''
if old not in s:
    raise SystemExit('scanContent lambda block not found')
s = s.replace(old, new)
imp.write_text(s)
