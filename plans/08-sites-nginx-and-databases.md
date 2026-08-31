# Etapa 08 — site-uri, Nginx, PHP-FPM și MariaDB

## Obiectiv și dependențe

Transformarea manifestelor de site în vhost-uri HTTP, pool-uri FPM per site și baze MariaDB declarate, fără side effects distructive.

**Depinde de:** 03–07.

## Flux `ncp link`

1. Parsează domeniul și flags.
2. Canonicalizează project path și document root.
3. Verifică precondițiile: Nginx, PHP și opțional MariaDB installed/running conform contractului.
4. Verifică paths, traversal permissions și handler-ul.
5. Construiește ID-ul stabil și manifestul propus.
6. Validează unicitatea domeniului/ID și snapshot-ul complet.
7. Generează pool FPM, Nginx vhost și DB provisioning.
8. Rulează tranzacția build/switch.
9. Verifică unități, socket și request local cu Host header.
10. Publică succesul human/JSON.

Nu instalează automat servicii sau PHP. Mesajele oferă comenzile necesare.

## Paths și defaults

| Caz | projectPath | documentRoot |
|---|---|---|
| fără `--path` | cwd canonic | project sau default template |
| Laravel | `--path`/cwd | `<project>/public` |
| WordPress | `--path`/cwd | `<project>` |
| fără template | `--path`/cwd | `<project>` |
| `--root=web` | project | `<project>/web` |
| `--root=/abs/web` | project | `/abs/web` |

`--template` și `--config` sunt mutual exclusive. Dacă ambele lipsesc, planul trebuie să aleagă un preset minimal static+PHP clar documentat; recomandare: handler `generic` intern cu `try_files $uri $uri/ =404` și PHP front controller nepresupus. Nu se ghicește framework-ul după fișiere.

## Pool PHP-FPM per site

- nume și socket derivate numai din site ID;
- binary/package PHP corespunde exact manifestului;
- extensions sunt compoziția globală compatibilă pentru versiune;
- procesul rulează ca owner NixCP;
- socket group este accesibil Nginx, mode restrictiv (de ex. `0660`);
- pool settings au defaults bounded și pot deveni configurabile ulterior numai prin schema validată;
- nu se partajează pool-uri între site-uri;
- logs identifică site ID fără a expune secrets.

Health-check confirmă existența socket-ului și execută un request reprezentativ fără a crea fișiere în proiect.

## Blocul Nginx deținut de Go

Go generează integral:

- `listen 80` și numai HTTP;
- `server_name` normalizat;
- `root`, index defaults;
- access/error logs;
- restricții pentru dotfiles/sensitive files;
- parametri FastCGI și socket FPM selectat;
- boundary-ul în care handler-ul poate defini reguli `location`.

Presetul/custom snippet **nu este un server block complet**. Se inserează în contextul server permis pentru location handling. Sunt interzise directivele `server`, `http`, `events`, `listen`, `server_name`, `root` (dacă ar suprascrie ownership-ul), orice `ssl`, certificate și ACME, `include` arbitrar și upstream-uri nevalidate.

Validarea custom are două niveluri:

1. lexer/parser/context allow/deny list care previne ieșirea din boundary;
2. test Nginx real în candidate build.

Regex simplu nu este suficient ca singura protecție.

## Preset Laravel

Cerințe:

```nginx
location / {
    try_files $uri $uri/ /index.php?$query_string;
}
```

Plus location PHP controlat care permite entrypoint-uri existente, pasează `SCRIPT_FILENAME` sigur și folosește socket-ul site-ului. Se blochează dotfiles și accesul la `.env`, storage privat și fișiere sensibile. Document root implicit este `public`.

## Preset WordPress

Cerințe:

```nginx
location / {
    try_files $uri $uri/ /index.php?$args;
}
```

PHP merge prin socket-ul site-ului. Se blochează `.ht*`, `wp-config.php` direct și fișiere sensibile. Nu se instalează WordPress și nu se editează `wp-config.php`.

## Custom snippet

- path absolut canonic și regular file, fără symlink;
- conținutul este citit/snapshot-uit în tranzacție și incorporat în Nix;
- schimbarea ulterioară a sursei nu afectează sistemul până la o comandă explicită de regenerate/relink în designul v1;
- erorile indică linia/directiva;
- orice TLS/SSL este hard error chiar dacă Nginx l-ar accepta.

## MariaDB per site

`--mariadb=example_app` înseamnă:

- asigură existența DB-ului validat;
- asigură un **account dedicat per-site**: user = numele DB-ului, cu o parolă
generată aleator (alfanumeric, `crypto/rand`), stocată numai în YAML-ul privat
al site-ului (0600);
- grants limitate la DB-ul declarat (CREATE USER + GRANT, idempotent, fără
  DROP);
- **nu pune parola în Nix-ul world-readable**: modulul generat referențiază un
  fișier privat `~/.nixcp/secrets/mariadb/accounts.sql` (0600) pe care unitatea
  oneshot `nixcp-mariadb-accounts` îl execută prin stdin; modulul poartă doar
  path-ul și un digest SHA-256 al SQL-ului (ca rotirea parolei să retrigereze
  `ALTER USER` la next switch);
- nu editează `.env`;
- nu presupune primary/shared și nu creează ownership între site-uri;
- aceeași DB menționată de mai multe manifesturi este respinsă
  (`database_in_use`) întrucât user = DB și un singur user per DB;

Dacă un site elimină referința ori este unlink-uit, DB și account/grants nu sunt
șterse automat în v1. Renderer-ul poate păstra un registry conservator al bazelor
create în config dacă este necesar pentru non-destrucție; decizia exactă trebuie
să garanteze că regenerarea nu emite un DROP. Comenzile destructive DB sunt în
afara scopului.

## `unlink`

- rezolvă domeniu sau ID neambiguu;
- elimină manifestul din snapshot candidat;
- elimină vhost/pool la switch;
- verifică Nginx după activare;
- nu șterge project path, logs istorice, DB, date, PHP ori extensii;
- no-op/unknown site este eroare clară sau `--if-exists` viitor; v1 preferă eroare pentru typo.

## List/show

`sites list` oferă ID, domeniu, enabled, PHP, root, template/custom și DB. `show` adaugă pool/socket, desired/actual health și paths. JSON folosește structuri, nu tabele serializate.

## Failure cases

- root lipsă/inaccesibil: fail înainte de build;
- port 80 ocupat de serviciu extern: health/switch failure și rollback;
- snippet invalid: candidate build failure, site-ul vechi rămâne activ;
- PHP-FPM socket absent: rollback;
- DB provisioning eșuat: rollback de config, fără drop;
- proiect mutat după link: doctor/status raportează path failure; nu caută automat alt path.

## Criterii de acceptanță

- două domenii pot folosi versiuni PHP diferite în pool-uri izolate;
- Laravel/WordPress front-controller routing funcționează;
- custom snippets valide funcționează, cele ce ies din context sunt respinse;
- numai HTTP/80 este generat;
- DB este creată/provizionată local fără primary/shared și fără password storage;
- unlink nu distruge fișiere sau DB;
- link failure păstrează configurația activă anterioară.
