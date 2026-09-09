package io.moondav.migrator;

import android.app.Activity;
import android.content.Intent;
import android.net.Uri;
import android.os.Bundle;
import android.provider.Settings;
import android.text.InputType;
import android.view.View;
import android.widget.*;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.*;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.*;
import java.util.concurrent.Executors;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import java.util.zip.InflaterInputStream;
import java.util.zip.ZipEntry;
import java.util.zip.ZipInputStream;
import java.util.zip.ZipOutputStream;

public class MainActivity extends Activity {
    private static final int PICK_BACKUP = 1001;
    private static final int CREATE_EXPORT = 1002;
    private Uri backupUri;
    private Analysis analysis;
    private final java.util.concurrent.ExecutorService executor = Executors.newSingleThreadExecutor();

    private TextView status, crypticView;
    private Button analyzeBtn, exportBtn, syncBtn;
    private EditText serverUrl, serverUser, serverPassword;

    @Override public void onCreate(Bundle b) {
        super.onCreate(b);
        setContentView(buildUi());
    }

    private View buildUi() {
        ScrollView scroll = new ScrollView(this);
        LinearLayout box = new LinearLayout(this);
        box.setOrientation(LinearLayout.VERTICAL);
        box.setPadding(dp(20), dp(20), dp(20), dp(32));
        scroll.addView(box);

        box.addView(text("Moon+ Reader → Readest Migrator", 24, true));
        TextView intro = text("Liest eine Moon+ Reader Sicherung als ZIP, extrahiert Lesepositionen und Markierungen und erzeugt Readest-Importdateien. Optional kann der Prozentfortschritt zu Calibre-Web Automated über KOReader Sync übertragen werden.", 16, false);
        intro.setPadding(0, dp(8), 0, dp(18));
        box.addView(intro);

        Button pick = new Button(this);
        pick.setText("1. Moon+ Sicherungs-ZIP auswählen");
        pick.setOnClickListener(v -> pickBackup());
        box.addView(pick);

        analyzeBtn = new Button(this);
        analyzeBtn.setText("2. Sicherung analysieren");
        analyzeBtn.setEnabled(false);
        analyzeBtn.setOnClickListener(v -> analyze());
        box.addView(analyzeBtn);

        exportBtn = new Button(this);
        exportBtn.setText("3. Readest-Migrationspaket exportieren");
        exportBtn.setEnabled(false);
        exportBtn.setOnClickListener(v -> createExport());
        box.addView(exportBtn);

        status = text("Noch keine Sicherung ausgewählt.", 15, false);
        status.setPadding(0, dp(16), 0, dp(12));
        box.addView(status);

        TextView crypticTitle = text("Kryptische Dateinamen", 18, true);
        crypticTitle.setPadding(0, dp(12), 0, dp(6));
        box.addView(crypticTitle);
        crypticView = text("Nach der Analyse erscheinen hier Hash-Dateien und ihre Lesepositionen.", 14, false);
        box.addView(crypticView);

        TextView cwaTitle = text("Optional: Fortschritt zu Calibre-Web Automated", 18, true);
        cwaTitle.setPadding(0, dp(22), 0, dp(8));
        box.addView(cwaTitle);
        box.addView(text("Calibre-Web Automated, kurz CWA, kann über seine KOReader-Sync-Schnittstelle Lesefortschritt speichern. Wenn die EPUB-Dateien im ZIP enthalten sind, berechnet die App denselben KOReader-Dateihash und überträgt die Moon+-Prozentposition.", 14, false));

        serverUrl = field("Server, z. B. https://books.example.de", false);
        serverUser = field("Calibre-Web Automated Benutzername", false);
        serverPassword = field("Passwort", true);
        box.addView(serverUrl); box.addView(serverUser); box.addView(serverPassword);

        syncBtn = new Button(this);
        syncBtn.setText("Fortschritt jetzt zu Calibre-Web Automated synchronisieren");
        syncBtn.setEnabled(false);
        syncBtn.setOnClickListener(v -> syncToCwa());
        box.addView(syncBtn);

        TextView note = text("Die App verändert weder die Moon+ Sicherung noch EPUB-Dateien. Sie schreibt nur eine neue Exportdatei bzw. sendet Fortschritt nach ausdrücklichem Tippen auf den Sync-Button.", 13, false);
        note.setPadding(0, dp(14), 0, 0);
        box.addView(note);
        return scroll;
    }

    private TextView text(String s, int sp, boolean bold) {
        TextView v = new TextView(this); v.setText(s); v.setTextSize(sp);
        if (bold) v.setTypeface(null, android.graphics.Typeface.BOLD);
        return v;
    }
    private EditText field(String hint, boolean password) {
        EditText e = new EditText(this); e.setHint(hint); e.setSingleLine(true);
        if (password) e.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_PASSWORD);
        return e;
    }
    private int dp(int n){ return Math.round(n * getResources().getDisplayMetrics().density); }

    private void pickBackup() {
        Intent i = new Intent(Intent.ACTION_OPEN_DOCUMENT);
        i.addCategory(Intent.CATEGORY_OPENABLE); i.setType("application/zip");
        startActivityForResult(i, PICK_BACKUP);
    }

    @Override protected void onActivityResult(int req, int res, Intent data) {
        super.onActivityResult(req,res,data);
        if (res != RESULT_OK || data == null || data.getData() == null) return;
        if (req == PICK_BACKUP) {
            backupUri = data.getData();
            try { getContentResolver().takePersistableUriPermission(backupUri, Intent.FLAG_GRANT_READ_URI_PERMISSION); } catch (Exception ignored) {}
            status.setText("Sicherung ausgewählt. Analyse kann gestartet werden.");
            analyzeBtn.setEnabled(true); exportBtn.setEnabled(false); syncBtn.setEnabled(false);
        } else if (req == CREATE_EXPORT) {
            Uri out = data.getData();
            if (out != null) exportBundle(out);
        }
    }

    private void analyze() {
        if (backupUri == null) return;
        busy(true, "Analysiere .po, .an, Namenslisten und EPUB-Verweise …");
        executor.execute(() -> {
            try {
                Analysis a = scanBackup();
                analysis = a;
                runOnUiThread(() -> {
                    String summary = a.positions.size()+" Lesepositionen, "+a.annotations.size()+" Annotation-Dateien, "+a.hashBooks.size()+" kryptische Bücher gefunden.";
                    StringBuilder sb = new StringBuilder();
                    if (a.hashBooks.isEmpty()) sb.append("Keine kryptischen Buchdateien gefunden.");
                    for (Position p : a.hashBooks) {
                        sb.append("• ").append(p.sourceFile).append("\n  ")
                          .append(p.title).append("\n  Position: ")
                          .append(p.percent == null ? "unbekannt" : fmt(p.percent)+" %")
                          .append("\n  Rohwert: ").append(p.raw).append("\n\n");
                    }
                    crypticView.setText(sb.toString().trim());
                    busy(false, summary);
                });
            } catch (Exception e) { fail("Analyse fehlgeschlagen: "+e.getMessage()); }
        });
    }

    private Analysis scanBackup() throws Exception {
        Analysis a = new Analysis();
        Map<String,String> titleByHash = new HashMap<>();
        Map<String,String> annotationBySource = new LinkedHashMap<>();
        Map<String,String> namesByBasename = new HashMap<>();

        try (InputStream raw = getContentResolver().openInputStream(backupUri); ZipInputStream zin = new ZipInputStream(new BufferedInputStream(raw))) {
            ZipEntry ze;
            while ((ze = zin.getNextEntry()) != null) {
                if (ze.isDirectory()) continue;
                String name = ze.getName(); String base = basename(name);
                if (base.equals("_names.list") || base.equals("recent.list")) {
                    String txt = new String(readLimited(zin, 8*1024*1024), StandardCharsets.UTF_8);
                    for (String line : txt.split("\\R")) {
                        int slash = Math.max(line.lastIndexOf('/'), line.lastIndexOf('\\'));
                        if (slash >= 0 && slash+1 < line.length()) namesByBasename.put(line.substring(slash+1).trim(), line.trim());
                    }
                } else if (base.endsWith(".an")) {
                    byte[] packed = readLimited(zin, 64*1024*1024);
                    String text = inflate(packed);
                    String source = base.substring(0, base.length()-3);
                    annotationBySource.put(source, text);
                    String hash = hashPrefix(source);
                    String title = embeddedTitle(text);
                    if (hash != null && !title.isEmpty()) titleByHash.put(hash, title);
                }
            }
        }
        a.annotations.putAll(annotationBySource); a.nameHints.putAll(namesByBasename);

        try (InputStream raw = getContentResolver().openInputStream(backupUri); ZipInputStream zin = new ZipInputStream(new BufferedInputStream(raw))) {
            ZipEntry ze;
            while ((ze = zin.getNextEntry()) != null) {
                if (ze.isDirectory()) continue;
                String base = basename(ze.getName());
                if (!base.endsWith(".po")) continue;
                String source = base.substring(0, base.length()-3);
                String po = new String(readLimited(zin, 4096), StandardCharsets.UTF_8).trim();
                Position p = Position.parse(source, po);
                String h = hashPrefix(source);
                if (h != null) {
                    p.cryptic = true;
                    if (titleByHash.containsKey(h)) { p.title = titleByHash.get(h); p.reconstructed = true; }
                    a.hashBooks.add(p);
                }
                a.positions.add(p); a.positionBySource.put(source.toLowerCase(Locale.ROOT), p);
            }
        }
        return a;
    }

    private void createExport() {
        Intent i = new Intent(Intent.ACTION_CREATE_DOCUMENT);
        i.addCategory(Intent.CATEGORY_OPENABLE); i.setType("application/zip");
        i.putExtra(Intent.EXTRA_TITLE, "moonplus-readest-migration.zip");
        startActivityForResult(i, CREATE_EXPORT);
    }

    private void exportBundle(Uri out) {
        if (analysis == null) return;
        busy(true, "Erzeuge Readest-Migrationspaket …");
        executor.execute(() -> {
            try (OutputStream os = getContentResolver().openOutputStream(out, "w"); ZipOutputStream zout = new ZipOutputStream(new BufferedOutputStream(os))) {
                writeZip(zout, "README.txt", buildReadme());
                writeZip(zout, "all_reading_positions.csv", csv(analysis.positions));
                writeZip(zout, "cryptic_books_manual_match.csv", csv(analysis.hashBooks));
                JSONObject manifest = new JSONObject();
                manifest.put("readingPositions", analysis.positions.size()); manifest.put("annotationFiles", analysis.annotations.size()); manifest.put("crypticBooks", analysis.hashBooks.size());
                JSONArray hashes = new JSONArray();
                for (Position p : analysis.hashBooks) hashes.put(p.toJson());
                manifest.put("crypticBookDetails", hashes);
                writeZip(zout, "migration_manifest.json", manifest.toString(2));
                Set<String> used = new HashSet<>();
                for (Map.Entry<String,String> e : analysis.annotations.entrySet()) {
                    String source=e.getKey(), title=normalizedTitleFor(source,e.getValue());
                    String fn=safe(title)+".mrexpt"; int n=2; while(!used.add(fn)) fn=safe(title)+"_"+(n++)+".mrexpt";
                    writeZip(zout, "mrexpt/"+fn, e.getValue());
                }
                busy(false, "Export abgeschlossen. Die ZIP-Datei kann direkt als Migrationsarchiv aufbewahrt werden.");
            } catch (Exception e) { fail("Export fehlgeschlagen: "+e.getMessage()); }
        });
    }

    private String buildReadme() {
        return "Moon+ Reader → Readest Migration\n\n"+
        "Dieses Paket wurde aus einer Moon+ Reader Sicherung erzeugt.\n\n"+
        "1. Markierungen und Notizen\n"+
        "   Im Ordner mrexpt/ liegen aus Moon+ Reader extrahierte .mrexpt-Dateien.\n"+
        "   In Readest das passende Buch öffnen, dann 'Import Annotations' → 'Moon+ Reader' wählen und die passende .mrexpt-Datei auswählen.\n\n"+
        "2. Lesepositionen\n"+
        "   all_reading_positions.csv enthält alle gefundenen Moon+ Reader .po-Positionen mit Zeitstempel, Kapitel bzw. Seite, Zeichenoffset und Prozentwert.\n"+
        "   Eine exakte Readest-EPUB-CFI wird nicht erfunden. Ohne identisches EPUB ist nur der gespeicherte Moon+-Fortschritt sicher bekannt.\n\n"+
        "3. Kryptische Dateinamen\n"+
        "   cryptic_books_manual_match.csv enthält Bücher mit 32-stelligen Hash-Dateinamen. Wenn der Titel aus einer Moon+ Annotation rekonstruiert werden konnte, steht er dort bereits ausgeschrieben.\n\n"+
        "4. Calibre-Web Automated (CWA)\n"+
        "   Calibre-Web Automated ist eine erweiterte Variante von Calibre-Web. Die Android-App kann optional Moon+-Prozentpositionen über die KOReader-Sync-Schnittstelle von Calibre-Web Automated übertragen. Dafür müssen dieselben EPUB-Dateien verfügbar sein, damit der KOReader-Dateihash übereinstimmt.\n\n"+
        "5. Sicherheit\n"+
        "   Die Sicherung und EPUB-Dateien werden nie verändert. Server-Zugangsdaten werden nur für den ausdrücklich gestarteten Sync verwendet und nicht in das Exportpaket geschrieben.\n";
    }

    private void syncToCwa() {
        if (analysis == null || backupUri == null) return;
        String base=serverUrl.getText().toString().trim(), user=serverUser.getText().toString().trim(), pass=serverPassword.getText().toString();
        if (base.isEmpty()||user.isEmpty()||pass.isEmpty()) { Toast.makeText(this,"Server, Benutzername und Passwort ausfüllen.",Toast.LENGTH_LONG).show(); return; }
        busy(true,"Berechne KOReader-Dateihashes und synchronisiere Fortschritt …");
        executor.execute(() -> {
            int found=0, sent=0, matched=0, failed=0;
            try (InputStream raw=getContentResolver().openInputStream(backupUri); ZipInputStream zin=new ZipInputStream(new BufferedInputStream(raw))) {
                ZipEntry ze;
                while((ze=zin.getNextEntry())!=null){
                    if(ze.isDirectory()) continue;
                    String source=basename(ze.getName());
                    if(!source.toLowerCase(Locale.ROOT).endsWith(".epub")) continue;
                    Position p=analysis.positionBySource.get(source.toLowerCase(Locale.ROOT));
                    if(p==null||p.percent==null) continue;
                    found++;
                    try {
                        String digest=partialMd5(zin);
                        CwaResult r=putCwa(base,user,pass,digest,p.percent);
                        if(r.ok){sent++; if(r.matched) matched++;} else failed++;
                    } catch(Exception ex){ failed++; }
                }
                busy(false,"Calibre-Web Automated Sync: "+sent+"/"+found+" übertragen, "+matched+" Calibre-Büchern direkt zugeordnet, "+failed+" fehlgeschlagen.");
            } catch(Exception e){ fail("Sync fehlgeschlagen: "+e.getMessage()); }
        });
    }

    private CwaResult putCwa(String base,String user,String pass,String doc,double percent) throws Exception {
        base=base.replaceAll("/+$",""); if(!base.toLowerCase(Locale.ROOT).endsWith("/kosync")) base += "/kosync";
        URL url=new URL(base+"/syncs/progress"); HttpURLConnection c=(HttpURLConnection)url.openConnection();
        c.setRequestMethod("PUT"); c.setDoOutput(true); c.setConnectTimeout(12000); c.setReadTimeout(12000);
        c.setRequestProperty("Authorization","Basic "+Base64.getEncoder().encodeToString((user+":"+pass).getBytes(StandardCharsets.UTF_8)));
        c.setRequestProperty("Accept","application/vnd.koreader.v1+json"); c.setRequestProperty("Content-Type","application/json");
        JSONObject body=new JSONObject(); body.put("document",doc); body.put("progress",fmt(percent)+"%"); body.put("percentage",percent/100.0); body.put("device","Moon+ Migrator"); body.put("device_id", Settings.Secure.getString(getContentResolver(),Settings.Secure.ANDROID_ID));
        try(OutputStream o=c.getOutputStream()){o.write(body.toString().getBytes(StandardCharsets.UTF_8));}
        int code=c.getResponseCode(); InputStream in=code>=200&&code<300?c.getInputStream():c.getErrorStream(); String resp=in==null?"":new String(readLimited(in,1024*1024),StandardCharsets.UTF_8);
        boolean matched=false; try{ matched=new JSONObject(resp).has("calibre_book_id"); }catch(Exception ignored){}
        return new CwaResult(code>=200&&code<300,matched);
    }

    private String partialMd5(InputStream in) throws Exception {
        long[] offs={0,1024,4096,16384,65536,262144,1048576,4194304,16777216,67108864,268435456,1073741824L};
        MessageDigest md=MessageDigest.getInstance("MD5"); byte[] buf=new byte[64*1024]; long pos=0; int idx=0,n;
        while((n=in.read(buf))!=-1 && idx<offs.length){
            long end=pos+n;
            while(idx<offs.length){
                long s=offs[idx], e=s+1024;
                if(s>=end) break;
                if(e<=pos){idx++;continue;}
                int from=(int)Math.max(0,s-pos), to=(int)Math.min(n,e-pos);
                if(to>from) md.update(buf,from,to-from);
                if(end>=e) idx++; else break;
            }
            pos=end;
        }
        StringBuilder sb=new StringBuilder(); for(byte b:md.digest()) sb.append(String.format(Locale.ROOT,"%02x",b)); return sb.toString();
    }

    private static byte[] readLimited(InputStream in,int max)throws IOException{ByteArrayOutputStream b=new ByteArrayOutputStream();byte[] x=new byte[8192];int n,total=0;while((n=in.read(x))!=-1){total+=n;if(total>max)throw new IOException("Datei zu groß");b.write(x,0,n);}return b.toByteArray();}
    private static String inflate(byte[] b)throws IOException{try(InflaterInputStream in=new InflaterInputStream(new ByteArrayInputStream(b))){return new String(readLimited(in,64*1024*1024),StandardCharsets.UTF_8);}}
    private static String embeddedTitle(String text){String n=text.replace("\r\n","\n").replace('\r','\n');int i=n.indexOf("\n#\n");if(i<0)return"";String[] lines=n.substring(i+3).split("\n",4);return lines.length>1?lines[1].trim():"";}
    private static String basename(String s){int i=Math.max(s.lastIndexOf('/'),s.lastIndexOf('\\'));return i>=0?s.substring(i+1):s;}
    private static String hashPrefix(String s){Matcher m=Pattern.compile("^([0-9a-fA-F]{32})\\.(epub|pdf|mobi|azw3|cbr)$").matcher(s);return m.matches()?m.group(1).toLowerCase(Locale.ROOT):null;}
    private static String normalizedTitleFor(String source,String an){String h=hashPrefix(source);String t=embeddedTitle(an);if(h!=null&&!t.isEmpty())return t;return source.replaceFirst("(?i)\\.(epub|pdf|mobi|azw3|cbr)$","");}
    private static String safe(String s){String r=s.replaceAll("[\\\\/:*?\"<>|\\p{Cntrl}]","_").trim();return r.length()>160?r.substring(0,160):r;}
    private static String fmt(double d){return String.format(Locale.ROOT,d==(long)d?"%.0f":"%.2f",d);}
    private static void writeZip(ZipOutputStream z,String name,String content)throws IOException{z.putNextEntry(new ZipEntry(name));z.write(content.getBytes(StandardCharsets.UTF_8));z.closeEntry();}
    private static String csv(List<Position> list){StringBuilder s=new StringBuilder("book,source_file,timestamp_ms,chapter_or_page,section,character_offset,percent,raw_position,cryptic_filename,title_reconstructed\n");for(Position p:list){s.append(q(p.title)).append(',').append(q(p.sourceFile)).append(',').append(p.timestamp==null?"":p.timestamp).append(',').append(p.chapter==null?"":p.chapter).append(',').append(p.section==null?"":p.section).append(',').append(p.offset==null?"":p.offset).append(',').append(p.percent==null?"":fmt(p.percent)).append(',').append(q(p.raw)).append(',').append(p.cryptic).append(',').append(p.reconstructed).append('\n');}return s.toString();}
    private static String q(String s){return "\""+(s==null?"":s.replace("\"","\"\""))+"\"";}

    private void busy(boolean on,String msg){runOnUiThread(()->{status.setText(msg);analyzeBtn.setEnabled(!on&&backupUri!=null);exportBtn.setEnabled(!on&&analysis!=null);syncBtn.setEnabled(!on&&analysis!=null);});}
    private void fail(String msg){runOnUiThread(()->{busy(false,msg);Toast.makeText(this,msg,Toast.LENGTH_LONG).show();});}

    static class CwaResult{final boolean ok,matched;CwaResult(boolean o,boolean m){ok=o;matched=m;}}
    static class Analysis{final List<Position> positions=new ArrayList<>(),hashBooks=new ArrayList<>();final Map<String,Position> positionBySource=new HashMap<>();final Map<String,String> annotations=new LinkedHashMap<>(),nameHints=new HashMap<>();}
    static class Position{
        String sourceFile,title,raw;Long timestamp;Integer chapter,section;Long offset;Double percent;boolean cryptic,reconstructed;
        static Position parse(String source,String raw){Position p=new Position();p.sourceFile=source;p.title=source.replaceFirst("(?i)\\.(epub|pdf|mobi|azw3|cbr)$","");p.raw=raw;
            Matcher m=Pattern.compile("^(\\d+)\\*(\\d+)@(\\d+)#(\\d+):([0-9.]+)%$").matcher(raw);if(m.matches()){p.timestamp=Long.parseLong(m.group(1));p.chapter=Integer.parseInt(m.group(2));p.section=Integer.parseInt(m.group(3));p.offset=Long.parseLong(m.group(4));p.percent=Double.parseDouble(m.group(5));return p;}
            m=Pattern.compile("^(\\d+)\\*(\\d+):([0-9.]+)%$").matcher(raw);if(m.matches()){p.timestamp=Long.parseLong(m.group(1));p.chapter=Integer.parseInt(m.group(2));p.percent=Double.parseDouble(m.group(3));return p;}
            m=Pattern.compile("([0-9.]+)%$").matcher(raw);if(m.find())p.percent=Double.parseDouble(m.group(1));return p;}
        JSONObject toJson(){JSONObject o=new JSONObject();try{o.put("sourceFile",sourceFile);o.put("title",title);o.put("percent",percent);o.put("raw",raw);o.put("titleReconstructed",reconstructed);}catch(Exception ignored){}return o;}
    }
}
