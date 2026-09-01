# Etapa 01 — scopul produsului și contractul CLI

## Obiectiv

Înghețarea suprafeței publice înainte de implementare. Etapa definește numele comenzilor, precondițiile, idempotency, rebuild-urile, output-ul machine-readable și codurile de ieșire.

**Dependențe:** niciuna.  
**Livrează:** un contract pe care etapele ulterioare nu îl schimbă accidental.

## Convenții globale

Toate comenzile acceptă:

```text
--json          un singur obiect JSON pe stdout, fără prompt și ANSI
--no-input      nu cere input; eșuează dacă lipsește o alegere
--yes           confirmă operațiile nedistructive care cer acord
--timeout       timeout pentru lock/operație, unde este relevant
```

`--json` implică `--no-input`. Output-ul uman merge pe stdout, diagnostics/progress pe stderr. În JSON, stdout conține exact un document; diagnostics sunt capturate în câmpuri structurate.

Envelope de succes:

```json
{
  "ok": true,
  "command": "php.install",
  "changed": true,
  "data": {"version": "8.4"},
  "warnings": []
}
```

Envelope de eroare:

```json
{
  "ok": false,
  "command": "link",
  "error": {
    "code": "php_version_not_installed",
    "message": "PHP 8.4 nu este instalat",
    "hint": "Rulează: ncp php install 8.4"
  },
  "warnings": []
}
```

Coduri de exit pe clase:

| Cod | Clasă |
|---:|---|
| 0 | succes, inclusiv no-op |
| 2 | sintaxă/usage |
| 3 | validare/precondiție |
| 4 | conflict sau lock timeout |
| 5 | platformă/permisiuni/sudo |
| 6 | evaluare/build Nix |
| 7 | switch/health-check |
| 8 | rollback incomplet |
| 9 | execuție runtime internă |

Pentru `ncp php ...`, `ncp artisan ...` și `ncp composer ...`, exit code-ul procesului PHP (respectiv al lui Composer) este propagat neschimbat; codurile de mai sus se aplică numai erorilor NixCP înainte de execuție.

## Arbore de comenzi

```text
ncp install
ncp status
ncp doctor
ncp service <nginx|mariadb|valkey> <install|start|status|stop|restart>
ncp <nginx|mariadb|valkey> <install|start|status|stop|restart>
ncp php install <version>
ncp php ext install <name>
ncp php use <version>
ncp php use --global <version>
ncp php [php-args...]
ncp artisan [artisan-args...]
ncp composer [composer-args...]
ncp a [artisan-args...]          # shortcut: ncp artisan <args…>
ncp am [artisan-migrate-args...]  # shortcut: ncp artisan migrate <args…>
ncp tinker [artisan-tinker-args...]  # shortcut: ncp artisan tinker <args…>
ncp ci [composer-install-args...]  # shortcut: ncp composer install <args…>
ncp link <domain> [flags]
ncp unlink <domain-or-site-id>
ncp sites list
ncp sites show <domain-or-site-id>
ncp shell init <bash|zsh|fish>
ncp skill
ncp tui
```

Aliasurile top-level pentru servicii folosesc exact același handler ca `ncp service`; nu există diferențe de stare sau output JSON.

## Contracte pe comenzi

### `ncp install`

- verifică NixOS, systemd, arhitectura, user non-root și tool-urile necesare;
- creează structura `~/.nixcp`, preseturi și modulele shell;
- generează modulul NixOS inițial;
- afișează importul manual exact pentru configurație clasică sau flake;
- nu editează `/etc/nixos`, flake-ul sau startup files;
- nu face primul switch înainte ca importul să fie confirmat/detectat;
- rerularea este no-op ori repară doar artefacte generate lipsă, fără a suprascrie manifestele valide.

Flag-uri planificate: `--flake <ref>` pentru rebuild-uri flake, `--impure` derivat/explicit unde importul absolut o cere, `--confirm-import` pentru validare după integrare.

### `ncp status` și `ncp doctor`

`status` oferă sumarul desired/actual pentru servicii, PHP și site-uri. `doctor` execută verificări aprofundate: owner/permisii, import Nix, toolchain, module evaluation, systemd, socket-uri, paths și drift. Sunt read-only și nu repară automat.

### Servicii

```text
ncp service mariadb install
ncp service valkey start
ncp nginx status
```

| Acțiune | Efect persistent | Rebuild | Reguli |
|---|---|---|---|
| `install` | `installed=true`, `desiredState=running` | da | idempotent |
| `start` | `desiredState=running` | da dacă se schimbă | necesită installed |
| `stop` | `desiredState=stopped` | da dacă se schimbă | păstrează pachet/config/date |
| `restart` | niciunul | nu | restart systemd controlat; necesită running |
| `status` | niciunul | nu | raportează desired, actual și drift |

Oprirea Nginx cu site-uri enabled este permisă, cu warning proeminent. Nu există `uninstall`/`purge` în v1.

### PHP install și extensii

```text
ncp php install 8.4
ncp php ext install redis
ncp php ext install opcache
```

- versiunile sunt normalizate (`8.4`, nu aliasuri ambigue);
- versiunea trebuie să existe în allowlist-ul nixpkgs suportat;
- instalarea aceleiași versiuni este no-op;
- extensia este rezolvată numai din extension set-ul nixpkgs;
- o extensie dorită se aplică tuturor versiunilor compatibile;
- combinațiile incompatibile sunt omise cu warning ce include extensia, versiunea și motivul;
- o extensie inexistentă în toate versiunile instalate este eroare de validare (protejează de typo);
- la instalarea unei versiuni noi se reaplică lista globală de extensii;
- nu se descarcă nimic din PECL/GitHub/URL-uri arbitrare.

### PHP use

```text
ncp php use 8.4
ncp php use --global 8.4
```

Local:

- versiunea trebuie instalată;
- scrie `<cwd>/.php-version`;
- cu integrarea shell activă, actualizează `PATH` și variabilele sesiunii curente;
- fără wrapper, scrie fișierul și afișează instrucțiunea exactă de activare, fără a pretinde că a schimbat shell-ul părinte;
- nu rulează rebuild.

Global:

- setează `php.globalDefault`;
- afectează terminalele noi, nu suprascrie terminalele deschise;
- nu scrie `.php-version`;
- rebuild doar dacă artefactele de sistem generate se schimbă; ideal default-ul CLI este pur user-state și nu cere rebuild.

### Execuție PHP și Artisan

```text
ncp php -v
ncp php script.php --flag value
ncp php ./vendor/bin/tool
ncp artisan make:controller UserController
ncp artisan tinker
ncp composer install --no-dev
ncp composer require laravel/ui
ncp a migrate:fresh --seed --force
ncp am --step=2
ncp tinker
ncp ci --prefer-dist --no-interaction
```

Argumentele sunt transmise identic, fără shell intermediar. `ncp artisan` cere ca `./artisan` să fie regular/readable și execută echivalentul `php ./artisan ...`. Semnalele și exit code-ul sunt propagate. `ncp composer` rulează același resolver PHP (calea stabilă din sesiunea/proiectul curent) peste scriptul sistem-own de la `/etc/nixcp/composer/bin/composer`; argumentele îi sunt transmise verbatim, iar exit code-ul Composer este returnat neschimbat — util pentru scripturi bazate pe Composer din proiecte PHP.

Shortcut-urile `a`, `am`, `tinker` și `ci` refolosesc exact aceleași code paths ca `ncp artisan`/`ncp composer`: orice argument sau flag adăugat după shortcut este trimis mai departe verbatim (prefixul fix — `migrate`, `tinker`, `install` — este injectat înaintea argv-ului utilizatorului), flag-urile globale NixCP (`--json`, `--timeout`, `--no-input`, `--yes`) sunt consumate de pre-run și niciodată scăpate în argv-ul copilului, TTY-ul este atașat pentru `tinker`, iar exit code-ul procesului copil se propagă neschimbat.

`ncp skill` tipărește referința completă de comenzi — toate căile, synopsis-urile, flag-urile și exemplele, plus footer-ul de flag-uri globale — într-un singur bloc text (sau JSON cu `--json`), dimensionat pentru a fi lipit în prompturi de unelte sau documentație.

`ncp tui` pornește panoul interactiv (5 taburi: Status/Sites/PHP/Services/Activity) doar pe TTY; refuză cu `tui_requires_tty` + exit 2 în non-TTY. Panoul nu implementează logică de business proprie: fiecare mutație rulează CLI-ul real in-process (cu `--json --no-input`), prin același pipeline validate → render → tranzacție blocată. `ncp` gol, interactiv, deschide panoul; în non-TTY rămâne banner-ul `NixCP CLI <version>` byte-stabil (contractul scripturilor).

### Link site

```text
ncp link example.test --php=8.4 --template=laravel --mariadb=example
ncp link blog.test --php=8.3 --template=wordpress --path=/srv/blog
ncp link custom.test --php=8.4 --config=/abs/site.locations.conf --root=web
```

Flag-uri:

- `--php` obligatoriu;
- `--mariadb` opțional;
- `--template=laravel|wordpress` ori `--config=<path>`, mutual exclusive;
- `--path`, implicit cwd;
- `--root`, relativ la project path sau absolut; se stochează forma canonică.

Precondiții: Nginx instalat, PHP instalat, MariaDB instalat dacă este cerut, path/root existente și accesibile, domeniu unic, snippet valid. Link generează manifest, vhost și pool FPM, apoi rebuild/switch/health-check. Nu instalează implicit servicii.

### Unlink și introspecție

`unlink` elimină doar vhost-ul, pool-ul și manifestul NixCP după rebuild reușit. Nu șterge proiectul, DB, datele MariaDB sau extensiile PHP. `sites list/show` sunt read-only și oferă echivalent JSON complet.

### Shell init

```text
ncp shell init bash
ncp shell init zsh
ncp shell init fish
```

Scrie exclusiv codul shell static pe stdout. Nu modifică automat `.bashrc`, `.zshrc` sau config fish.

## Confirmări și UX

- `huh` se folosește numai într-un TTY și numai când o alegere reală lipsește;
- operațiile declarative, idempotente nu cer confirmare inutilă;
- rollback-ul și erorile sunt explicate acționabil;
- `lipgloss` este dezactivat pentru non-TTY, `NO_COLOR` și `--json`;
- niciun warning nu transformă un rezultat reușit în exit nonzero.

## Criterii de acceptanță

- toate comenzile apar coerent în `ncp help`;
- aliasurile de serviciu au comportament identic;
- fiecare comandă declară precondiții, side effects și rebuild;
- JSON este un singur document și are error codes stabile;
- no-op returnează succes cu `changed:false`;
- nu există comandă/flag pentru funcțiile excluse.
