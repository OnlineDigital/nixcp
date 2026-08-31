# Etapa 03 — modelul de stare și schemele YAML

## Obiectiv și dependențe

Definirea sursei adevărului, a serializării stricte, a validărilor locale/cross-file și a validării stricte a versiunii schemei.

**Depinde de:** 01–02.  
**Deblochează:** renderer-ul Nix și toate mutațiile persistente.

## Principii

- YAML este authoritativ; modulul Nix este derivat.
- Un singur `config.yaml`, un fișier separat per site.
- Decode strict: unknown fields și duplicate keys sunt erori.
- Encode determinist: ordine stabilă, liste sortate unde ordinea nu are semantică, newline final.
- Versiunile PHP sunt întotdeauna string-uri quoted (`"8.4"`), pentru a evita conversii numerice.
- Întreg snapshot-ul este validat înainte de orice mutație.
- Nu se acceptă symlink-uri pentru fișierele managed.

## `config.yaml` v2

```yaml
schemaVersion: 2
owner:
  username: alice
  uid: 1000
  group: users
  gid: 100
  home: /home/alice
platform:
  system: x86_64-linux
rebuild:
  mode: traditional          # traditional | flake
  target: null               # ex. .#hostname pentru flake
  impure: false
  importConfirmed: false
services:
  nginx:
    installed: false
    desiredState: stopped    # running | stopped
  mariadb:
    installed: false
    desiredState: stopped
  valkey:
    installed: false
    desiredState: stopped
php:
  installed: []
  extensions: []
  globalDefault: null
```

Owner-ul este capturat la install și nu este o opțiune liber editabilă fără reinitializare controlată. `rebuild.impure` este validat în funcție de strategia de import; nu devine o metodă de a injecta argumente arbitrare.

## Manifest site

Laravel:

```yaml
schemaVersion: 2
id: example-com
enabled: true
domain: example.com
projectPath: /home/alice/src/example
documentRoot: /home/alice/src/example/public
php: "8.4"
mariadb:
  database: example_app
nginx:
  handler:
    type: template
    name: laravel
```

WordPress fără DB gestionată explicit:

```yaml
schemaVersion: 2
id: blog-example-com
enabled: true
domain: blog.example.com
projectPath: /home/alice/src/blog
documentRoot: /home/alice/src/blog
php: "8.3"
nginx:
  handler:
    type: template
    name: wordpress
```

Custom:

```yaml
schemaVersion: 2
id: app-example-com
enabled: true
domain: app.example.com
projectPath: /home/alice/src/app
documentRoot: /home/alice/src/app/web
php: "8.4"
nginx:
  handler:
    type: custom
    path: /home/alice/src/app/nixcp.locations.conf
```

`mariadb` este opțional. Nu există `primary`, `shared`, parent/worktree sau ownership semantic. Mai multe site-uri pot menționa același nume de DB fără ca NixCP să inventeze o relație între ele.

## Normalizare și validare

### Domenii

- lowercase și trailing dot eliminat înainte de stocare;
- se acceptă hostname ASCII valid; IDN poate fi acceptat doar după conversie explicită, deterministă la punycode;
- se resping scheme, slash/path, query, fragment, port, whitespace și wildcard;
- unicitate case-insensitive după normalizare;
- fără generare DNS sau `/etc/hosts`.

### Site ID

Slug stabil din domeniul normalizat: caractere non-alfanumerice devin `-`; la coliziune se adaugă hash scurt determinist. Rename domain este în v1 unlink+link, nu mutație implicită a identității.

### Paths

- `projectPath`, `documentRoot` și custom config sunt absolute și canonice la link;
- project/root trebuie să existe; root trebuie să fie director;
- custom config trebuie să fie regular/readable;
- root relativ se rezolvă sub project path; root absolut este permis explicit;
- se verifică traversal/read permissions pentru Nginx și FPM;
- validarea symlink-urilor și a path-urilor world-writable este tratată în etapa 09.

### PHP/extensii/DB

- versiune format `major.minor` și prezentă în allowlist;
- extension name lowercase într-un charset restrâns și rezolvabil în nixpkgs;
- numele DB respectă un subset sigur MariaDB (`[A-Za-z0-9_]+`, limită documentată), fără quoting arbitrar;
- `globalDefault`, dacă există, trebuie să fie instalat.

## Validări cross-file

Snapshot-ul este invalid dacă:

- există domenii ori IDs duplicate;
- un site enabled cere Nginx neinstalat;
- PHP-ul unui site nu este instalat;
- un site declară DB cu MariaDB neinstalat;
- handler-ul template nu există;
- handler-ul custom nu trece validarea de context;
- owner/platforma nu corespund execuției curente;
- un serviciu neinstalat are `desiredState: running`;
- lista de extensii sau PHP conține duplicate după normalizare.

Comanda care propune o stare intermediar invalidă trebuie să ofere o eroare precondiție, nu să repare implicit alte resurse.

## Layout și permisiuni

- root managed/directoare: `0700`;
- config/site/generated/shell files: `0600`;
- template-urile pot fi `0600`, deoarece conținutul este incorporat în modul la generare;
- fișierele trebuie să aparțină UID-ului owner;
- lock și journals: `0600`.

Fișierul `.php-version` este în proiect și nu face parte din snapshot-ul `~/.nixcp`; conține exact o versiune normalizată și newline.

## Versiunea schemei

- fiecare document are `schemaVersion: 2`;
- loader-ul și mutațiile resping orice altă versiune fără a modifica starea;
- generated Nix se regenerează din starea validată.

## Chei interzise prin design

Schema nu are loc pentru TLS/certificate/ACME, DNS, email, web UI, worktree, DB primary/shared, alte DB-uri, alte servicii sau PECL. Unknown-field rejection face ca asemenea încercări să eșueze explicit.

## Pași de implementare

1. Definește structs separate pentru documente și modele normalizate.
2. Implementează decoder strict, duplicate-key detection și limite de mărime.
3. Implementează normalizarea și validatorii de domeniu.
4. Implementează loader-ul snapshot integral.
5. Implementează cross-validation.
6. Implementează encoder-ul canonic și ID generation.
7. Validează strict schemaVersion: 2 pentru toate documentele de stare.
8. Adaugă ownership/perms hooks pentru etapa 09.

## Criterii de acceptanță

- exemplele se decodează și round-trip-uiesc deterministic;
- unknown/duplicate fields sunt respinse;
- starea invalidă nu poate ajunge la renderer;
- ordinea fișierelor/listelor nu schimbă bytes generați;
- nu există câmpuri pentru funcțiile excluse;
- fixtures de migrare și cross-validation sunt complete.
