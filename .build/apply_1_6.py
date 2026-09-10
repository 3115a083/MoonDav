from pathlib import Path
import base64,zlib
root=Path("/tmp/src/MoonMigration")
files={
'app/build.gradle.kts':'eNp...TRUNCATED_PLACEHOLDER',
}
for rel,data in files.items():
    p=root/rel; p.parent.mkdir(parents=True,exist_ok=True); p.write_bytes(zlib.decompress(base64.b64decode(data)))
