from pathlib import Path
root=Path('/tmp/src/MoonMigration')
p=root/'app/src/main/java/dev/moonmigration/app/MainActivity.kt'
s=p.read_text()
s=s.replace('scrollbarStyle = View.SCROLLBARS_INSIDE_INSET','setScrollBarStyle(View.SCROLLBARS_INSIDE_INSET)')
s=s.replace('val article=Regex("^(?i)(the|a|an|der|die|das|den|dem|des|ein|eine|einer|eines|einem|einen|le|la|les|un|une|el|los|las)\\s+")','val article=Regex("^(the|a|an|der|die|das|den|dem|des|ein|eine|einer|eines|einem|einen|le|la|les|un|une|el|los|las)\\\\s+", RegexOption.IGNORE_CASE)')
p.write_text(s)
