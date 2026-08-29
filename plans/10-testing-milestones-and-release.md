# Etapa 10 — testare, milestone-uri și release

## Obiectiv și dependențe

Validarea completă de la modele Go până la un NixOS VM activ și definirea ordinii de livrare/release.

**Depinde de:** toate etapele anterioare.

## Piramida de testare

### Unit tests Go

- strict YAML, duplicate keys, round-trip și migrations;
- domenii, IDs, paths, DB identifiers;
- PHP version resolution și `.php-version` precedence;
- extension compatibility și warning aggregation;
- Nix escaping și rendering determinist;
- shell quoting/PATH manipulation;
- state machine servicii;
- JSON envelopes/error mapping;
- transaction state machine și fault injection;
- no-op detection.

Rulare cu race detector pentru pachetele de state/transaction.

### Golden tests

Fixtures versionate pentru:

1. modul gol;
2. fiecare serviciu separat running/stopped;
3. toate serviciile;
4. PHP 8.3 + 8.4;
5. extensii complet și parțial compatibile;
6. Laravel;
7. WordPress;
8. generic/custom snippet;
9. multiple site-uri/pool-uri;
10. site cu MariaDB;
11. escaping adversarial.

Golden update este comandă explicită și diff-ul este review-uit.

### CLI integration

Cu HOME temporar și adapters fake:

- bootstrap/rerun;
- fiecare comandă și alias;
- idempotency și `changed:false`;
- precondiții și hints;
- lock contention/timeouts;
- failure în fiecare transaction phase;
- recovery din journal stale;
- non-TTY, `--json`, `--no-input`;
- output unic și fără ANSI;
- PHP/Artisan argv/exit code;
- unlink non-destructiv.

### Shell integration real

Containere nu fac parte din produs, dar CI poate folosi procese/sandbox-uri de test; ideal testele pornesc executabilele reale bash/zsh/fish într-un mediu temporar.

- source init;
- wrapper delegation;
- local activation + direct `php -v`;
- două shell-uri simultane cu versiuni diferite;
- global default numai pentru sesiuni noi;
- PATH dedup;
- invalid activation atomic;
- spaces/metacharacters/injection;
- TTY pentru Artisan tinker unde este fezabil.

### Nix evaluation

Față de o revizie nixpkgs fixată în CI:

- modul gol și toate golden states evaluează;
- mapping-urile PHP există;
- extension availability este derivată corect;
- unsupported pair este omis cu warning;
- assertion `x86_64-linux`;
- package closures includ PHP-urile selectate;
- nu există ACME/TLS options;
- candidate harness testează candidatul real.

### NixOS VM tests

Scenarii obligatorii:

1. import/bootstrap și config gol;
2. lifecycle Nginx + reboot;
3. lifecycle MariaDB + data păstrată;
4. lifecycle Redis + bind local;
5. două site-uri cu versiuni PHP diferite;
6. Laravel front-controller;
7. WordPress front-controller;
8. custom snippet valid și invalid;
9. MariaDB DB + Unix-socket account/grants;
10. Redis `PING` local și lipsă listener public;
11. stop/start persistă după reboot;
12. build Nginx invalid nu înlocuiește site-ul funcțional;
13. health-check failure declanșează rollback;
14. kill/recovery în faza published;
15. răspuns HTTP anterior rămâne după rollback.

Testele verifică listeners cu instrumente din VM și folosesc Host headers; nu configurează DNS sau TLS.

## CI

Pipeline propus:

1. `gofmt`/lint/import boundaries;
2. unit + race + fuzz smoke;
3. integration + shell matrix;
4. golden/Nix evaluation;
5. NixOS VM tests;
6. `govulncheck`, SBOM/checksums;
7. build reproducibil `ncp` pentru `x86_64-linux`;
8. release numai dacă toate sunt verzi.

Nixpkgs este pin-uit pentru CI/release. O matrice periodică față de o revizie mai nouă detectează drift-ul, dar nu schimbă automat suportul.

## Milestone-uri

### M0 — contract freeze

Documentele 00/01/03 aprobate: scop, comenzi, scheme, non-goals și invarianta HTTP.

### M1 — fundație CLI

Cobra, DI, output JSON/human, errors, fakes și build metadata. `help/version/status` funcționează.

### M2 — state engine

Layout, strict YAML, validators, ownership checks, migrations, deterministic encoding.

### M3 — renderer și bootstrap

`ncp install`, import traditional/flake, modul gol, deterministic Nix, candidate evaluation.

### M4 — safety core

Lock, staging, journal, build/switch/verify/rollback și fault-injection. **Gate:** niciun feature persistent nu trece mai departe fără M4 verde.

### M5 — servicii

Nginx/MariaDB/Redis lifecycle complet, desired/actual status, local-only health.

### M6 — PHP

Multi-version CLI/FPM composition, extensions nixpkgs și stable paths.

### M7 — shell/execution

`use` local/global, bash/zsh/fish, terminale paralele, PHP/Artisan execution.

### M8 — site-uri

Link/unlink/list/show, pool per site, presets/custom, MariaDB declarations.

### M9 — hardening și beta

Security corpus, VM suite, doctor, docs operaționale și release candidate.

### M10 — v1.0

Toate acceptance criteria, upgrade/migration test, package/release/checksums și changelog.

## Release artifacts

- binar `ncp` pentru NixOS `x86_64-linux`;
- preferabil flake/package Nix pentru instalarea binarului, fără a impune Home Manager;
- checksum și provenance/SBOM;
- completions Cobra;
- manpage ori docs CLI generate;
- preset-uri embedded versionate;
- ghid import traditional/flake;
- ghid shell init bash/zsh/fish;
- troubleshooting/rollback/doctor;
- matrice exactă PHP/nixpkgs suportată.

## Compatibilitate și upgrade

- semantic versioning pentru CLI și schema;
- schema migration înainte de mutație, cu backup;
- generated module se regenerează;
- versiunea veche de binar refuză schema nouă;
- release-ul fixează/testează o bază nixpkgs și documentează politica de actualizare;
- nu se promite suport pentru PHP scos din nixpkgs.

## Definition of done v1

- bootstrap non-root + import unic funcțional;
- toate mutațiile folosesc tranzacție și rollback;
- 3 servicii × 5 acțiuni verificate;
- PHP CLI/FPM multiple și extensions nixpkgs;
- incompatibilități parțiale sunt warnings;
- terminale paralele folosesc direct versiuni PHP diferite;
- PHP/Artisan păstrează argv/signals/exit;
- site-uri multi-PHP cu FPM pools separate;
- Laravel/WordPress/custom funcționale numai pe HTTP;
- MariaDB și Redis local-only;
- unlink/stop nu distrug date;
- `--json` stabil, unic și prompt-free;
- eșecurile nu lasă drift tăcut;
- testele Go, shell, Nix și NixOS VM sunt verzi.

## Riscuri care trebuie validate devreme

1. **Candidate build cu import absolut/flake:** prototip în M3, înainte de features.
2. **Installed + stopped în modulele NixOS:** VM spike per serviciu în M5.
3. **PHP attributes/extensions în nixpkgs:** catalog generat/testat în M6.
4. **Parent-shell PATH:** shell matrix în M7, nu doar unit tests.
5. **Nginx custom context safety:** parser + real config test înainte de beta.
6. **MariaDB socket grants declarative:** spike; fallback oneshot idempotent.

Niciun risc nu este „rezolvat” doar prin documentație; fiecare are test/prototip și gate explicit.
