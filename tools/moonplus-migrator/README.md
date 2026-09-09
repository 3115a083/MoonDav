# Moon+ Reader → Readest Migrator

A small Android migration utility for moving reading data away from Moon+ Reader without modifying the original backup.

## What the app migrates

- Moon+ Reader `.po` reading positions
- Moon+ Reader `.an` annotations
- recovered titles for hash-named books when the title is embedded in annotation data
- a manual-match report for books whose title cannot be reconstructed safely
- Readest-compatible `.mrexpt` annotation files

The app accepts a Moon+ Reader backup ZIP and writes a separate migration ZIP.

## Readest import

Readest already understands Moon+ Reader `.mrexpt` annotation exports.

Open the corresponding book in Readest and choose:

**Import Annotations → Moon+ Reader**

Then select the matching file from the exported `mrexpt/` directory.

The migration bundle also contains `all_reading_positions.csv`. It preserves the Moon+ timestamp, chapter or page, section, character offset and percentage. The app does not invent an EPUB CFI when the exact EPUB structure is unavailable.

## Calibre-Web Automated

**Calibre-Web Automated**, abbreviated **CWA** after this first definition, is an extended distribution of Calibre-Web.

The app can optionally send Moon+ reading percentages to Calibre-Web Automated through its KOReader Sync endpoint. This is useful when Readest, KOReader and Calibre-Web Automated should share the same progress.

For this to map to the existing Calibre book, the same EPUB bytes must be present in the selected backup. The app calculates KOReader's partial-MD5 document identifier directly from the EPUB and sends the progress to Calibre-Web Automated.

Nothing is uploaded until the user explicitly presses the sync button.

## Files in the migration ZIP

- `mrexpt/`: Readest-importable Moon+ annotations
- `all_reading_positions.csv`: all recovered reading positions
- `cryptic_books_manual_match.csv`: hash-named books and their positions
- `migration_manifest.json`: machine-readable summary
- `README.txt`: end-user import instructions

## Privacy and safety

- The source backup is read-only.
- EPUB files are never modified.
- Server credentials are not written into the migration ZIP.
- Network access is used only for the optional, user-triggered Calibre-Web Automated sync.
- No ebook cache is created by the app.

## Build

The project targets Android 8.0 and newer, API 26+.

A debug APK is built by the dedicated GitHub Actions workflow on the isolated build branch.
