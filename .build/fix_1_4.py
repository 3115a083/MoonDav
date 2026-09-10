from pathlib import Path
root=Path('/tmp/src/MoonMigration')

# Kotlin/API compatibility fixes from the initial 1.4 patch.
p=root/'app/src/main/java/dev/moonmigration/app/MainActivity.kt'
s=p.read_text()
s=s.replace('scrollbarStyle = View.SCROLLBARS_INSIDE_INSET','setScrollBarStyle(View.SCROLLBARS_INSIDE_INSET)')
s=s.replace('val article=Regex("^(?i)(the|a|an|der|die|das|den|dem|des|ein|eine|einer|eines|einem|einen|le|la|les|un|une|el|los|las)\\s+")','val article=Regex("^(the|a|an|der|die|das|den|dem|des|ein|eine|einer|eines|einem|einen|le|la|les|un|une|el|los|las)\\\\s+", RegexOption.IGNORE_CASE)')
p.write_text(s)

# Moon+ books DB uses cover/thumb in the supported schema, not coverFile/thumbFile.
p=root/'app/src/main/java/dev/moonmigration/app/MrproImporter.kt'
s=p.read_text()
s=s.replace('SELECT book, filename, lowerFilename, author, description, coverFile, thumbFile FROM books ORDER BY book COLLATE NOCASE','SELECT book, filename, lowerFilename, author, description, cover, thumb FROM books ORDER BY book COLLATE NOCASE')
s=s.replace('c.str("coverFile")?.takeIf{it.isNotBlank()} ?: c.str("thumbFile")?.takeIf{it.isNotBlank()}','c.str("cover")?.takeIf{it.isNotBlank()} ?: c.str("thumb")?.takeIf{it.isNotBlank()}')
p.write_text(s)

# Make the right-side alphabet a draggable fast-scroller as well as a tap index.
p=root/'app/src/main/java/dev/moonmigration/app/MainActivity.kt'
s=p.read_text()
old='''            val alphabet=LinearLayout(this).apply { orientation=LinearLayout.VERTICAL; gravity=Gravity.CENTER_HORIZONTAL; setPadding(dp(4),0,0,0) }
            for(letter in ('A'..'Z')) alphabet.addView(TextView(this).apply { text=letter.toString(); textSize=12f; gravity=Gravity.CENTER; minWidth=dp(30); minHeight=dp(24); setOnClickListener { alphabetAnchors[letter]?.requestRectangleOnScreen(android.graphics.Rect(0,0,alphabetAnchors[letter]!!.width,alphabetAnchors[letter]!!.height),true) } })
            listRow.addView(alphabet, LinearLayout.LayoutParams(dp(34),ViewGroup.LayoutParams.WRAP_CONTENT))
'''
new='''            val alphabet=LinearLayout(this).apply { orientation=LinearLayout.VERTICAL; gravity=Gravity.CENTER_HORIZONTAL; setPadding(dp(4),0,0,0) }
            fun jumpToLetter(letter: Char) {
                val target = alphabetAnchors[letter] ?: run {
                    val next = ('A'..'Z').firstOrNull { it >= letter && alphabetAnchors.containsKey(it) }
                    next?.let { alphabetAnchors[it] }
                }
                target?.requestRectangleOnScreen(android.graphics.Rect(0,0,target.width,target.height),true)
            }
            for(letter in ('A'..'Z')) alphabet.addView(TextView(this).apply { text=letter.toString(); textSize=12f; gravity=Gravity.CENTER; minWidth=dp(30); minHeight=dp(24); setOnClickListener { jumpToLetter(letter) } })
            alphabet.setOnTouchListener { view, event ->
                when(event.actionMasked) {
                    android.view.MotionEvent.ACTION_DOWN, android.view.MotionEvent.ACTION_MOVE -> {
                        val h=view.height.coerceAtLeast(1)
                        val index=((event.y.coerceIn(0f,(h-1).toFloat())/h)*26f).toInt().coerceIn(0,25)
                        jumpToLetter(('A'.code+index).toChar())
                        true
                    }
                    else -> false
                }
            }
            listRow.addView(alphabet, LinearLayout.LayoutParams(dp(38),ViewGroup.LayoutParams.WRAP_CONTENT))
'''
if old not in s: raise SystemExit('alphabet block not found')
s=s.replace(old,new)
p.write_text(s)
