from pathlib import Path
import re
p=Path('/tmp/src/MoonMigration/app/src/main/java/dev/moonmigration/app/EpubPositionResolver.kt')
s=p.read_text()
pat=re.compile(r'''    private fun parseXml\(bytes: ByteArray\) = try \{.*?    \} catch \(_: Throwable\) \{ null \}\n''', re.S)
new='''    private fun parseXml(bytes: ByteArray): org.w3c.dom.Document? {
        val probe = bytes.toString(Charsets.UTF_8).lowercase(java.util.Locale.ROOT)
        if ("<!doctype" in probe || "<!entity" in probe) return null
        return try {
            val f = DocumentBuilderFactory.newInstance().apply {
                isNamespaceAware = false
                try { setFeature("http://apache.org/xml/features/disallow-doctype-decl", true) } catch (_: Throwable) { }
                try { setFeature("http://xml.org/sax/features/external-general-entities", false) } catch (_: Throwable) { }
                try { setFeature("http://xml.org/sax/features/external-parameter-entities", false) } catch (_: Throwable) { }
                try { setFeature("http://apache.org/xml/features/nonvalidating/load-external-dtd", false) } catch (_: Throwable) { }
                try { setAttribute("http://javax.xml/XMLConstants/property/accessExternalDTD", "") } catch (_: Throwable) { }
                try { setAttribute("http://javax.xml/XMLConstants/property/accessExternalSchema", "") } catch (_: Throwable) { }
            }
            f.newDocumentBuilder().parse(ByteArrayInputStream(bytes))
        } catch (_: Throwable) { null }
    }
'''
s2,n=pat.subn(new,s,count=1)
if n != 1:
    raise SystemExit('parseXml generated block not found')
p.write_text(s2)
