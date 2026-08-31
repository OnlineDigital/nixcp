# Etapa 02 — arhitectura Go și fundația CLI

## Obiectiv și dependențe

Construirea scheletului executabilului `ncp`, a limitelor dintre pachete și a infrastructurii de output/test fără logică NixOS specifică.

**Depinde de:** etapa 01.  
**Deblochează:** state engine, renderer, servicii, PHP și site-uri.

## Stack

- Go stabil, module Go și `cmd/ncp` ca singur entry point;
- Cobra pentru parsing/help/completion;
- `gopkg.in/yaml.v3` direct, fără Viper;
- `go-playground/validator` plus validări de domeniu proprii;
- `charmbracelet/huh` numai pentru input interactiv;
- `charmbracelet/lipgloss` numai pentru prezentare human;
- standard library pentru procese, path-uri, semnale și JSON unde este suficient.

Versiunile dependențelor se fixează în `go.mod`/`go.sum`; actualizarea lor este explicită.

## Structură recomandată

```text
cmd/ncp/main.go
internal/cli/          # construcția Cobra, flags, completions
internal/command/      # use-case orchestration
internal/state/        # modele YAML și loader
internal/site/         # reguli site/domain/path
internal/php/          # versiuni, extensii, active resolution
internal/service/      # lifecycle desired/actual
internal/nix/          # renderer determinist și escaping
internal/rebuild/      # nixos-rebuild/systemctl
internal/transaction/  # lock, staging, journal, rollback
internal/shell/        # bash/zsh/fish protocol
internal/output/       # human/JSON și error mapping
internal/platform/     # NixOS/systemd/user checks
internal/execx/        # exec argv-safe și signal propagation
internal/testutil/     # fakes, fixtures, temp HOME
```

`internal/command` depinde de interfețe mici. Adaptoarele concrete sunt injectate în `main`; nu există singletons globale și nu se ascunde stare în environment variables.

## Composition root

`main`:

1. creează `context` anulat de SIGINT/SIGTERM;
2. construiește filesystem/process/platform adapters;
3. rezolvă user-ul real fără a accepta root;
4. construiește store, renderer, transaction manager și use-case-uri;
5. construiește arborele Cobra;
6. execută și mapează eroarea la output/exit code exact o dată.

Cobra nu scrie erori automat în paralel cu renderer-ul aplicației (`SilenceUsage`, `SilenceErrors`). Usage apare numai la erori de sintaxă.

## Interfețe de test

Interfețe minime, nu abstractions speculative:

- `FileStore`: load snapshot, stage, publish, restore;
- `Locker`: shared/exclusive acquire cu context;
- `Renderer`: state validat → bytes Nix;
- `CommandRunner`: argv, cwd, env controlat, stdout/stderr;
- `PrivilegeRunner`: allowlist strict pentru rebuild/systemctl;
- `Systemd`: active/enabled/restart/status;
- `Rebuilder`: preflight/build/switch/rollback/current generation;
- `HealthChecker`: verificări pe resurse schimbate;
- `Clock` și generator de transaction IDs pentru teste deterministe.

Nu se creează o interfață generică de pluginuri pentru servicii.

## Model de erori

Erorile interne sunt typed și includ:

```text
Code, Message, Hint, Cause, Details, ExitClass
```

Se păstrează cauza pentru logs/tests, dar nu se expune automat stack intern în JSON. Erorile subprocess includ argv redactat, exit code și output bounded. Nu se loghează viitoare secrets.

Warnings sunt valori structurate și se agregă de-a lungul use-case-ului; nu sunt printate ad-hoc din pachetele de domeniu.

## Output

### Human

- stdout: rezultat și tabele;
- stderr: progress, warning și diagnostics;
- culori numai dacă stdout/stderr relevante sunt TTY și `NO_COLOR` lipsește;
- mesajele trebuie să spună ce s-a schimbat, ce nu și pasul următor.

### JSON

- exact un obiect pe stdout;
- fără ANSI, spinner, prompt sau linii auxiliare;
- output subprocess limitat și inclus în `data`/`error.details`;
- ordinea logică a câmpurilor este stabilă pentru lizibilitate, fără a transforma ordinea într-un API semantic;
- timestamps doar unde sunt necesare și în RFC3339 UTC.

## Execuția proceselor

- se folosește `exec.CommandContext`, niciodată `sh -c` pentru comenzi formate din input;
- argv este o listă explicită;
- environment-ul este construit controlat;
- pentru PHP/Artisan se face signal forwarding și exit-code propagation;
- output-ul lung de rebuild se stream-uiește în modul human și se capturează bounded pentru JSON;
- anularea contextului nu ucide procesul înainte de a permite cleanup/rollback controlat.

## UX interactiv

`huh` este permis pentru selectarea unei versiuni instalate sau confirmarea unei operații clar delimitate. Dacă stdin nu este TTY, comanda nu improvizează defaults periculoase. `--json` și `--no-input` dezactivează prompturile.

## Pași de implementare

1. Inițializează modulul Go și dependențele.
2. Creează composition root și root command.
3. Adaugă version/build metadata.
4. Definește result/error/warning envelopes.
5. Implementează renderer human/JSON și mapping exit codes.
6. Definește interfețele OS și fakes.
7. Adaugă context/signal handling.
8. Creează comenzile placeholder `status`, `doctor`, `shell init` cu contract corect.
9. Adaugă lint, format, unit tests și completions Cobra.

## Teste

- parsing și validarea combinațiilor de flags;
- exact un JSON object pe succes/eroare;
- fără ANSI în JSON/non-TTY;
- no prompt în `--json`;
- mapping error→exit code;
- signal forwarding în fake runner;
- output subprocess bounded;
- Cobra nu dublează errors/usages;
- dependențele de domeniu nu importă UI packages (verificare statică/lint).

## Criterii de acceptanță

- `ncp help`, `ncp version`, `ncp status --json` rulează;
- toate comenzile din contract pot fi înregistrate fără implementare duplicată;
- adaptoarele externe pot fi înlocuite integral în teste;
- human și JSON folosesc același rezultat semantic;
- nu există Viper, shell command construction sau stare globală implicită.
