# NixCP — plan general

## 1. Scop

NixCP este un panou de hosting **exclusiv CLI** pentru NixOS, implementat în Go și expus prin comanda globală `ncp`. El gestionează declarativ, pentru un singur utilizator local:

- Nginx;
- versiuni multiple PHP CLI și PHP-FPM;
- extensii PHP disponibile în nixpkgs;
- MariaDB;
- Redis;
- site-uri HTTP cu pool PHP-FPM separat și preset Nginx Laravel/WordPress ori snippet custom.

Starea dorită este în `~/.nixcp/*.yaml`. Go validează starea și generează un modul NixOS; schimbările de sistem sunt activate prin `sudo nixos-rebuild`. Fișierele YAML sunt sursa adevărului, nu configurația Nix generată.

## 2. Constrângeri definitive

| Aspect | Decizie |
|---|---|
| Platformă | NixOS, `x86_64-linux`, systemd |
| Utilizatori | un singur owner NixCP per mașină în v1 |
| Implementare | Go; Nix și shell doar ca artefacte generate |
| CLI | Cobra, `huh`, `lipgloss`, `go-playground/validator`, `yaml.v3` |
| Config | YAML simplu, strict; fără Viper |
| Privilegii | CLI rulat fără root; `sudo` numai pentru operații controlate |
| Web | Nginx, HTTP pe portul 80 |
| PHP | pachete/extensii exclusiv din nixpkgs; CLI și FPM multi-versiune |
| Date | MariaDB; Redis local |
| Automatizare | output uman și contract stabil `--json` pentru scripturi/agenți AI |

### Invariantă HTTP-only

NixCP nu va implementa niciodată TLS/SSL, HTTPS, ACME, Let's Encrypt sau certificate self-signed. Câmpurile și directivele aferente sunt respinse, nu ignorate.

## 3. Non-obiective permanente

Nu se implementează: email, phpMyAdmin, DNS local/public, `/etc/hosts`, web UI, worktrees, relații DB primary/shared, alte baze de date, alte cache-uri/servicii, Apache/Caddy, containere, Home Manager, extensii PECL ori surse arbitrare, editarea automată a surselor aplicației/`.env`, editarea automată a configurației NixOS sau a fișierelor shell startup.

„Extensibil” în v1 înseamnă separarea internă a domeniilor și adaptoarelor, nu pluginuri publice și nu acceptarea altor servicii.

## 4. Arhitectură

```text
utilizator
   │
   ▼
 ncp / Cobra ───► validare + use-case-uri ───► output human / JSON
   │                         │
   │                         ├──► ~/.nixcp/config.yaml
   │                         ├──► ~/.nixcp/sites/*.yaml
   │                         └──► renderer Nix determinist
   │                                      │
   │                                      ▼
   │                     ~/.nixcp/generated/nixcp-module.nix
   │                                      │ import manual, o singură dată
   └──► transaction/rebuild ──────────────┴──► sudo nixos-rebuild
                                                   │
                                                   ▼
                                 Nginx / PHP-FPM / MariaDB / Redis
```

Straturi:

1. **UI CLI**: Cobra, prompturi TTY opționale, output.
2. **Use-case-uri**: install, servicii, PHP, site-uri.
3. **Domeniu/stare**: modele, validări și invariants cross-file.
4. **Generare**: Nix, template-uri Nginx și shell integration.
5. **Adaptoare OS**: filesystem sigur, lock, procese, systemd, rebuild.

Dependența este într-un singur sens: UI → use-case → domeniu/interfețe → adaptoare. Domeniul nu importă Cobra, `huh` sau `lipgloss`.

## 5. Structura `~/.nixcp`

```text
~/.nixcp/
├── config.yaml
├── sites/
│   └── <site-id>.yaml
├── nginx-templates/
│   ├── laravel.conf
│   └── wordpress.conf
├── generated/
│   └── nixcp-module.nix
├── shell/
│   ├── bash.sh
│   ├── zsh.sh
│   └── fish.fish
├── backups/
├── transactions/
└── lock
```

Directoarele sunt `0700`; fișierele de stare și cele generate sunt `0600`. Artefactele publicate sunt atomice și reproductibile. Fișierele suspecte (symlink, owner greșit, permisiuni nesigure) blochează mutația.

## 6. Suprafața CLI propusă

```text
ncp install [--flake <ref>] [--json]
ncp status | doctor

ncp service nginx|mariadb|redis install|start|status|stop|restart
ncp nginx|mariadb|redis install|start|status|stop|restart  # alias ergonomic

ncp php install <8.x>
ncp php ext install <nume>
ncp php use <8.x>
ncp php use --global <8.x>
ncp php [argumente PHP...]
ncp artisan [argumente Artisan...]

ncp link <domeniu> --php=<8.x> [--mariadb=<db>]
         [--template=laravel|wordpress | --config=<snippet>]
         [--path=<proiect>] [--root=<document-root>]
ncp unlink <domeniu|site-id>
ncp sites list
ncp sites show <domeniu|site-id>

ncp shell init bash|zsh|fish
```

Comenzile persistente sunt idempotente și trec prin aceeași tranzacție. `restart` este operațional și nu schimbă starea declarată. `status` compară starea dorită cu starea systemd reală.

## 7. Selecția PHP

`ncp php use 8.4` are două efecte:

1. scrie atomic `.php-version` în proiectul curent;
2. prin wrapper-ul instalat pentru shell, modifică `PATH` în **shell-ul părinte curent**.

Un proces copil nu poate modifica mediul părintelui. Din acest motiv, integrarea bash/zsh/fish definește o funcție `ncp` care interceptează strict comanda `php use`, cere binarului cod shell sigur și îl evaluează numai la exit code 0. Restul comenzilor sunt delegate binarului real.

`ncp php use --global 8.4` schimbă default-ul capturat de terminalele noi; nu rescrie `PATH` în terminalele deja deschise. Astfel două terminale pot păstra simultan versiuni diferite.

Ordinea de rezoluție pentru `ncp php` și `ncp artisan` este:

1. cel mai apropiat `.php-version` din directorul curent sau părinți;
2. `NIXCP_PHP_VERSION` din sesiune;
3. default global;
4. eroare structurată dacă nu există o versiune activă.

## 8. Site-uri

Fiecare domeniu are manifest separat, document root canonic, versiune PHP instalată și pool FPM/socket propriu. Go generează întregul bloc Nginx `server`: `listen 80`, `server_name`, root, loguri, indici, restricții comune și legarea la socket. Presetul sau snippet-ul custom este inserat numai în zona controlată pentru location handling.

- `--path` are default directorul curent;
- Laravel are implicit root `public`;
- WordPress are implicit root `.`;
- `--root` suprascrie default-ul;
- `--template` și `--config` sunt mutual exclusive;
- serviciile și versiunea PHP trebuie instalate explicit înainte de link;
- `--mariadb` declară o bază, fără ownership primary/shared;
- unlink nu șterge proiectul, baza sau datele.

## 9. Siguranță și tranzacții

Orice mutație persistentă:

1. obține lock exclusiv;
2. încarcă și validează integral starea curentă;
3. construiește starea propusă în memorie;
4. generează YAML și Nix candidate în staging pe același filesystem;
5. execută preflight/evaluare/build pe candidat;
6. publică atomic starea și modulul;
7. rulează `sudo nixos-rebuild switch`;
8. verifică unitățile și endpoint-urile relevante;
9. finalizează jurnalul tranzacției;
10. la eșec, restaurează snapshot-ul și generația precedentă.

Implementarea exactă a build-ului candidat trebuie să țină cont de modul traditional versus flake; nu se publică o soluție care testează accidental vechiul modul importat. Toate execuțiile folosesc argv explicit, fără concatenare într-un shell.

## 10. Etape și dependențe

```text
01 contract produs
   └─► 02 fundație Go/CLI
         └─► 03 stare YAML
               └─► 04 instalare + renderer Nix
                     └─► 05 tranzacții/rebuild
                           ├─► 06 servicii
                           ├─► 07 PHP + shell
                           └─► 08 site-uri/Nginx/DB
                                  └─► 09 hardening
                                        └─► 10 teste/release
```

Etapele 06–08 pot avansa parțial în paralel, dar integrarea lor nu începe înainte ca renderer-ul determinist și rollback-ul să fie testate.

## 11. Definition of done

Produsul v1 este gata când:

- bootstrap-ul și importul manual unic funcționează pe NixOS `x86_64-linux`;
- mutațiile sunt locked, idempotente, rebuild-safe și rollback-capable;
- cele trei servicii au toate cele cinci operații cerute;
- mai multe versiuni PHP CLI/FPM coexistă;
- extensiile nixpkgs compatibile se aplică tuturor versiunilor, iar incompatibilitățile produc warnings;
- două terminale pot folosi direct `php` cu versiuni diferite;
- Laravel, WordPress și snippet-uri custom validate funcționează pe HTTP;
- fiecare site folosește pool-ul și versiunea FPM corecte;
- MariaDB și Redis nu sunt expuse public;
- output-ul `--json` este unic, stabil, fără ANSI/prompturi;
- testele unitare, integration, shell, evaluare Nix și NixOS VM sunt verzi;
- niciun eșec de build/switch nu lasă starea declarată și sistemul activ în divergență tăcută.

## 12. Documente pe etape

- [01 — scop și contract CLI](01-product-scope-and-command-contract.md)
- [02 — arhitectură Go și fundație CLI](02-go-architecture-and-cli-foundation.md)
- [03 — stare și scheme YAML](03-state-model-and-yaml-schemas.md)
- [04 — instalare și generare modul NixOS](04-installation-and-nixos-module-generation.md)
- [05 — tranzacții, lock și rebuild](05-transactions-locking-and-rebuilds.md)
- [06 — servicii de sistem](06-system-services.md)
- [07 — PHP, extensii și integrare shell](07-php-versions-extensions-and-shells.md)
- [08 — site-uri, Nginx și MariaDB](08-sites-nginx-and-databases.md)
- [09 — securitate, permisiuni și validare](09-security-permissions-and-validation.md)
- [10 — testare, milestone-uri și release](10-testing-milestones-and-release.md)
