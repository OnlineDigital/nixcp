# Etapa 11 — TUI interactiv (`ncp tui`, alias: `ncp` gol)

## Obiectiv și dependențe

Un TUI bazat pe `charmbracelet/bubbletea` (v1.3.4, deja în go.mod indirect prin huh; va deveni dependență directă) care expune același panou de control ca și CLI-ul, într-un singur ecran cu taburi. Rularea `ncp` fără argumente pornind `ncp tui`.

**Depinde de:** 01–10 (toate use-case-urile existente).  
**Deblochează:** nimic — strat de prezentare pur peste use-case-urile existente.

## Principii

1. **Nu logică nouă.** TUI-ul nu validează stare, nu generează Nix, nu rulează rebuild direct. Orice acțiune invocă exact funcțiile use-case pe care le folosește și CLI-ul (`runService`, `mutatePHP`, `runLink`, `runUnlink`, `runComposer`-style helpers). Domeniul rămână nesenzat: `internal/tui` importă `command`/`state`/`service`/`php`, niciodată invers.
2. **Un singur mod de a schimba sistemul.** YAML rămâne sursa adevărului; acțiunile TUI trec prin aceleași tranzacții lockate cu rollback ca și CLI. Dacă TUI-ul moare, starea nu poate rămâne în divergență (tranzacția are lock + restore).
3. **No-TTY = refuz explicit.** `ncp tui` (și `ncp` gol) verifică `term.IsTerminal(stdin)` + stdout; fără TTY → mesaj de eroare acționabil (`"ncp tui requires an interactive terminal"`), niciodată UI-ul nu pornește în non-TTY sau sub `--json`. Comportamentul CLI existent nu se schimbă: `ncp --version`, `ncp help` etc. rămân byte-stabile.
4. **Refresco-graceful.** `r` reîncarcă totul; blocarea pe o eroare de citire (ex: `not_configured`) afișează un ecran de onboarding, nu un crash.

## Comportamentul `ncp` gol

```go
// în root.go, RunE-ul root-ului:
if commandJSON(cmd) { /* rămâne cum e: versiune JSON */ }
if TTY && terminal capabil → lansează TUI
altfel → print versiune (comportamentul actual, pentru scripturi)
```

`ncp tui` este o comandă separată, vizibilă în help; `ncp` fără argumente devine un alias al ei doar pe TTY. Astfel `ncp` în pipe/script rămâne netedeterminist (versiune + exit 0).

## Arhitectura pachetului `internal/tui`

```text
internal/tui/
├── tui.go        # model-ul bubbletea de nivelul 1: root model, tab routing, keymap global
├── keys.go       # keymap-ul global + help (bubbles/help)
├── theme.go      # paleta lipgloss centralizată (reutilizează NO_COLOR/termenv din internal/ui)
├── status/       # tab 1 — Status (dashboard)
├── sites/        # tab 2 — Sites
├── php/          # tab 3 — PHP
├── services/     # tab 4 — Services
├── logs/         # tab 5 — Activity (jurnalul de acțiuni din sesiune)
└── actions/      # executorul de acțiuni asincrone (vezi mai jos)
```

Modelul bubbletea clasic: `RootModel` deține `activeTab int`, instanțe ale fiecărui tab-model, și un `ActionRunner` comun. Fiecare tab implementează o interfață minimală:

```go
type Tab interface {
    Name() string
    Update(tea.Msg) (Tab, tea.Cmd)   // tab-ul își poate întoarce o nouă versiune de sine
    View() string
    Refresh(ctx context.Context) tea.Cmd  // produce Msg cu date reîncărcate
}
```

Mesajele cross-tab (`SitesChangedMsg`, `ServicesChangedMsg`, `PHPChangedMsg`) declanșează re-încărcarea taburilor afectate — după o acțiune reușită, toate taburile văd starea nouă fără restart.

## Executorul de acțiuni (partea delicată)

Asemenea CLI-ului, acțiunile sunt **sincrone și blocante** (nixos-rebuild durează). În TUI ele rulează într-un `tea.Cmd` (goroutine), cu:

- **`WaitMsg`**: spinner (bubbles/spinner) + faza curentă ("building", "switching", "health check") — fazele provin din `transaction.Manager` via callback-uri `PhaseNotify(phase string)`;
- **anulare `Ctrl+C`**: context cancel → tranzacția face rollback-ul ei controlat (nu SIGKILL); UI-ul revine, jurnalul din tabul Activity arată faza la care s-a anulat;
- **rezultat**: `ActionDoneMsg{err *errors.AppError, warnings, phase}` → afișare inline în status bar + intrare în Activity; niciodată popup-uri TTY raw (`huh` nu e compatibil cu bucla bubbletea fără grijă — confirmările se implementează ca un mic mod „confirm overlay" proprietar: `Are you sure? [y/n]`, nu form huh încapsulat).

Confirmările au două niveluri: acțiuni reversibile (install PHP, start/stop service) → fără confirmare, direct; acțiuni distructive/with-rebuild (uninstall PHP, unlink site) → overlay de confirmare.

## Layout (foartefix) 

```text
┌─ NixCP ───────────────────────────────────────────────────────────┐
│ [1]Status  [2]Sites  [3]PHP  [4]Services  [5]Activity              │  ← header tabs
├───────────────────────────────────────────────────────────────────┤
│                                                                   │
│                    (corpul tabului activ)                          │
│                                                                   │
│                                                                   │
├───────────────────────────────────────────────────────────────────┤
│ ↑↓ select · Enter action · a install · d delete · r refresh       │  ← context bar
│ q quit · 1-5 tab · ? more help                                     │  ← global keys
└───────────────────────────────────────────────────────────────────┘
```

- taburi: taste `1`–`5` și `←/→`/`Tab`/`shift-tab`;
- corpul fiecărui tab este un `bubbles/table` sau `bubbles/list` + panou de detalii (viewport) pe partea dreaptă sau dedesubt;
- footer-ul de două rânduri: rândul 1 = acțiuni contextuale pentru tabul activ; rândul 2 = taste globale;
- help-ul extins (`?`) folosește `bubbles/help` cu short/full toggle;
- redimensionare: `tea.WindowSizeMsg` → re-render; lățimea taburilor și tabelelor se adaptează, minim 80×24 (sub asta: mesaj "terminal too small").

## Conținutul taburilor

### Tab 1 — Status (dashboard read-only)

Ce se vede (rând cu rând, din `store.Load()` + `runtime.Services.Status` + `sitepkg.RealChecker`):

- **NixCP**: configured/nu, schema version, owner, cale stare (`~/.nixcp`), rebuild mode + target;
- **Servicii** (tabel 3 rânduri): nume, iconiță stare (`●` running / `○` stopped / `✗` not installed), desired vs actual, drift (dacă `actual != desired` → rândul devine galben + `! drift`);
- **PHP**: versiunile instalate (badge-uri), globalDefault (stea ★), extensii active (count + expand cu detaii la `Enter` pe rând);
- **Site-uri**: count + ultimele N (sau toate dacă încap): domeniu, enabled, PHP, handler; site-urile down (proba HTTP locală eșuează) marcate roșu;
- **Sănătate sumară**: o linie verde/galbenă/roșie — „All systems nominal" / „2 services drifted" etc.

Taste: `r` refresh; `g` goto (Enter pe un site/service → sare în tabul respectiv cu acel element selectat); nimic de modificat aici.

### Tab 2 — Sites (listă + detalii + acțiuni)

- **Lista** (bubbles/table): domeniu, PHP, handler, enabled, db (dacă există);
- **Detalii** (viewport dreapta): toate câmpurile SiteConfig — projectPath, documentRoot, socket FPM, socket path, MariaDB user/db (parola nu se afișează niciodată — se arată `••••` și o instrucțiune `ncp sites show <domain>`, respectând contractul de securitate: prin TTY ok, dar TUI-ul nu o re-render-ează accidental la resize), health probe (socket + HTTP, buton `p` re-probe), nginx template/snippet info;
- **Acțiuni** (context bar): 
  - `a` — **link site nou** (form overlay proprietar): domain (input), php version (select din instalate), template (laravel/wordpress/generic/custom path input), mariadb (optional input), path (default cwd), root (default `public` pentru laravel/wordpress, altfel project root) — cu validare inline per câmp, mesaj de eroare sub câmpul invalid, nu submit blocat;
  - `d` pe un site — **unlink** (confirm overlay: arată ce se șterge — doar vhost/pool/manifest — și ce rămâne — proiect/DB/date);
  - `e` — **enable/disable** site (toggle în manifest + rebuild);
  - `o` — **open** (cu browser? nu — v1 nu are comenzi „open"; în schimb: `c` = copiază URL-ul `http://domain` în clipboard via OSC52? — scope-creep; skip: afișează URL-ul în detalii, gata);
  - `p` — re-probe health acum.
- Dacă `snap.Config.Services.Nginx` nu e installed/running → tabul afișează empty-state cu mesaj „Nginx is required to link sites. Press `s` to go to Services and install it." (legături între taburi = superputerea TUI-ului).

### Tab 3 — PHP (versiuni + extensii)

- **Secțiunea versiuni** (table): versiune, stare (`installed` / `available`), GlobalDefault ★, extensii count, dimensiune aproximativă? (nu — nu există sursă; skip);
- Acțiuni pe versiuni:
  - `a` — install versiune nouă (listă cu cele din `php.Catalog` neinstalate încă; confirma → mutatePHP(action=install));
  - `u` — use/set default (local vs global: overlay cu două opțiuni — `use` scrie `.php-version` în cwd dacă e proiect, `use --global` schimbă default-ul; TUI-ul cere explicit care);
  - `x` — uninstall (confirm overlay; refuzul `php_version_in_use` se afișează frumos cu hintul „used by linked site X");
- **Secțiunea extensii** (al doilea table sau listă în panoul de detalii al versiunii selectate):
  - extensii instalate (checkmark) + disponibile din catalog pentru versiunile instalate;
  - `a` — install extensie (select din `php.Catalog` compatibile cu versiunile instalate; warnings de compatibilitate `php.CompatibilityWarnings` se afișează în Activity, nu blochează);
  - `x` pe extensie — uninstall? **Nu** — v1 contractul CLI nu are `php ext uninstall`; TUI-ul nu inventează acțiuni care nu există în CLI. Afișăm extensia cu nota „managed via config.yaml".

### Tab 4 — Services (nginx, mariadb, valkey)

- Tabel: service, installed, desired state, actual (active/enabled/health), drift;
- Acțiuni per serviciu (Enter deschide un meniu vertical sau taste directe):
  - `i` — install (dacă nu e);
  - `s` — start;
  - `x` — stop (confirm overlay dacă nginx are site-uri enabled — reutilizăm exact warning-ul `nginx_stopped_with_enabled_sites`);
  - `r` — restart (numai dacă installed + desired running, exact ca `runService`);
  - mereu vizibil: unit name + buton de status refresh;
- Toate trec prin `runService` → tranzacție + rebuild + health check; output de fază în Activity + spinner.

### Tab 5 — Activity (jurnal sesiune)

- Lista cronologică a tuturor acțiunilor din sesiunea TUI curentă: timestamp, acțiune, țintă, rezultat (ok/changed:false/failed/cancelled), warnings, faza tranzacției la final/anulare, și — pentru eșecuri — `error.code` + hint;
- `Enter` pe o intrare → viewport cu detalii complete (stderr bounded, argv redactat — exact ce produce `appErrDiagnostics`);
- Nu persistă nicăieri — doar sesiune. E și „stderr-ul" TUI-ului: orice eroare de sistem (load store eșuat, systemd probe eșuat) intră aici + status bar.

## Empty states & onboarding

- **Not configured** (`not_configured` la load): ecran dedicat — „NixCP is not initialized. Run `ncp install` from a terminal, then come back." + tastele r/q. TUI-ul nu rulează `ncp install` din interior (instalarea cere editare manuală de configuration.nix — pas uman obligatoriu).
- **No sites**: tabul 2 afișează „No sites linked yet. Press `a` to link your first site."
- **No PHP installed**: tabul 3 oferă direct acțiunea `a` (install) cu primul pas evidențiat.
- **Nginx not running**: banner galben în Status + Sites.

## Detalii de implementare în etape

### Etapa A — fundația (root model, taburi, theme, actions runner)

1. `go.mod`: bubbletea + bubbles devin dependențe directe (`go get github.com/charmbracelet/bubbletea@v1.3.4 github.com/charmbracelet/bubbles@v0.21.0`);  deja rezolvate în go.sum prin huh.
2. `internal/tui/tui.go` — `RootModel`, routing `Update`/`View`, `WindowSizeMsg` handling, keymap global (1-5, ←/→, tab, q, r, ?),` tea.WithAltScreen()`, `tea.WithMouseCellMotion()` opțional dezactivat până se dovedește util;
3. `internal/tui/theme.go` — culori adaptate `termenv` (respectă `NO_COLOR`, non-TTY nu ajunge aici oricum), reutilizează logica `internal/ui.renderer()`;
4. `internal/tui/actions/runner.go` — executorul cu faze + anulare; ca să prindem fazele tranzacției fără să refactorizăm `transaction.Manager` observabil: în v1 implementăm un `PhaseReporter` opțional în `Runtime` (`Runtime.PhaseReporter func(phase string)`), setat de TUI; CLI-ul nu-l setează și rămâne cu outputul curent — zero risc de regresie;
5. `ncp tui` comandă nouă în `internal/command/tui.go` + root alias pe TTY;
6. Taburile A: toate afișează date reale read-only (reutilizând `loadPHP`/`collectServiceStatus`/`siteStore` helpers), fără acțiuni.

**Testare etapă A:** build + `ncp tui` pe un config existent; teste unitare pentru `RootModel.Update` (tab switching, quit, resize minim).

### Etapa B — acțiuni Services + PHP

1. Executorul hookup cu `runService`/`mutatePHP` (via callback-uri în Runtime, ca să nu importeze `command` din `internal/tui` — invers: `command` construiește `tui.RootModel` cu adaptere funcționale);
2. Overlay-uri: confirm (y/n), select simplu, input text (bubbles/textinput) — toate proprietare, nu huh;
3. Spinner + faze + anulare Ctrl+C cu rollback controlat;
4. Activity tab funcțional (jurnalul acțiunilor).

**Testare etapă B:** acțiuni pe state home de test (fake transactions la fel ca `service_precond_test.go`); teste pentru: mesajele de succes/eșec/anulare; jurnalul complet.

### Etapa C — acțiuni Sites

1. Form overlay de link (validare per câmp cu `state.NormalizeDomain` etc., inline);
2. Unlink cu confirm overlay (textul explică exact ce rămâne/ce dispare);
3. Enable/disable toggle;
4. Health probe on-demand.

### Etapa D — polish

1. Cross-tab goto (`g` din Status), empty states complete, banner-uri (drift, nginx down);
2. `bubbles/help` full toggle;
3. Mouse opțional (click pe taburi/rânduri) dacă nu complică;
4. README + CHANGELOG + docs/testing-and-release.md (matricea de testare: un rând TUI).

## Decizii de evitat (scope-ul v1)

- **nu** editare directă YAML din TUI (config.yaml rămâne editabil doar prin comenzi/acțiuni valide);
- **nu** rulare artisan/composer din TUI (sunt passthrough-uri de proces — nu au sens într-un UI și ar strica contractul TTY al celorlalte flaguri);
- **nu** vizualizare logs journald (require privilegii/sintaxă nouă; scope-creep);
- **nu** setări „rebuild mode" editabile din TUI (rebuild.target e configurat la install; a-l muta din UI ar ascunde un pas care cere reverificare NixOS);
- **nu** theming user-selectable, mai multe panouri simultan, etc.

## Impact pe contractul existent

- `ncp` fără argumente pe TTY: **schimbare de comportament** — acum pornește TUI în loc să printeze versiunea. Documentată în CHANGELOG. În non-TTY (scripturi, CI): rămâne exact `NixCP CLI <version>` pe stdout, exit 0.
- `ncp tui` nou — apare în help, cu `Args: cobra.NoArgs`, refuză non-TTY cu exit code `ExitCodeUsage` + mesaj clar.
- `--json` + `ncp` gol → rămâne envelope JSON (TUI-ul nu se poate porni sub --json, deci nu colizionează).
- Zero modificări la state/transaction/domain — totul prin use-case-uri existente.

## Status implementare (final)

Implementat în `internal/tui` + `internal/command/tui_backend.go`, cu trei
abateri conștiente față de schița de mai sus, toate în favoarea simplității:

1. **Fără `Runtime.PhaseReporter`** — în loc să extindem Runtime-ul, mutațiile
   TUI rulează CLI-ul real in-process (root cobra proaspăt per acțiune, cu
   `--json --no-input --timeout=1800s`) și parsăm envelope-ul JSON de succes
   (including `data.phase` și `warnings`). Zero refactorizări în command și
   zero risc de regresie pe CLI; faza tranzacției apare în Activity din
   envelope. Anularea Ctrl+C funcționează prin contextul cancellable al
   acțiunii (rollback-ul tranzacției desfășoară starea parțială).
2. **Fără dependența `bubbles`** — overlay-urile (confirm/select/form) sunt
   proprietare, minimal implementate în `internal/tui/overlay.go`
   (textinput inclus); doar `bubbletea` + `lipgloss` rămân dependențe directe.
3. **Site enable/disable a fost exclus** — nu există comandă CLI
   corespunzătoare (`link`/`unlink` sunt singurele mutații de site), deci a
   inventa un toggle în TUI ar fi încălcat principiul „zero logică nouă de
   business". Tastele `e`/toggle nu există; `p`/enter reprobează health-ul.

Punctele rămase din plan care NU sunt implementate (acceptate ca depășite de
schiță): PhaseReporter live (faza se citește post-factum din envelope),
`g` cross-tab goto, mouse, `bubbles/help` toggle. Toate criteriile de
acceptanță de mai jos sunt însă îndeplinite (verificat cu teste).

## Criterii de acceptanță

- `ncp` (TTY) pornește TUI-ul; `q`/Ctrl+C iese curat (fără terminal corupt);
- toate cele 5 taburi afișează date reale; `r` reîncarcă fără flicker;
- install PHP/service din TUI produce exact același YAML final ca și CLI-ul (test de echivalență pe snapshot-uri);
- anularea unui rebuild lasă starea consistentă (rollback tranzacției, jurnalul notează faza anulării);
- non-TTY: `ncp` → versiune ca înainte; `ncp tui` → eroare acționabilă;
- teste unitare: tab routing, overlay-uri confirm/cancel, acțiuni cu fake runner/transactions, jurnal Activity;
- `make check` verde; `gofmt`/`vet` curate.

## Notă despre versiunea bubbletea

go.mod are deja bubbletea v1.3.4 ca dependență indirectă. Folosim fix această versiune (nu v2/lipgloss v2) ca să nu forțăm upgrade huh/lipgloss. API-ul v1 (tea.Model, tea.Cmd, tea.Msg) e stabil și potrivit pentru acest scope.
