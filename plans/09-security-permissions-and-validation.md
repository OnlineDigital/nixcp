# Etapa 09 — securitate, permisiuni și validare

## Obiectiv și dependențe

Hardening al întregului control plane înainte de release: filesystem, sudo, Nix/Nginx/shell injection, expunere de rețea și separarea site-urilor.

**Depinde de:** 03–08.

## Model de amenințări

- YAML modificat manual ori malițios;
- duplicate/unknown fields care schimbă interpretarea;
- Nix string/path injection;
- Nginx context escape din custom snippet;
- shell code injection prin versiuni/path-uri;
- symlink swap și TOCTOU în `~/.nixcp` sau proiect;
- schimbarea owner-ului/permisilor;
- argv/sudo injection;
- proiecte sub directoare world-writable;
- domenii care produc host/header confusion;
- expunere publică MariaDB/Redis;
- acces cross-site la socket-uri FPM;
- execuția accidentală ca root;
- output/log-uri care expun date sensibile.

## Trust boundaries

1. Input CLI/YAML/custom Nginx este neîncredere.
2. Fișierele proiectului sunt user-owned, dar se pot schimba concurent.
3. Renderer-ele Nix/shell/Nginx sunt boundaries de escaping distincte.
4. `sudo`/systemd/Nix sunt boundary privilegiat.
5. Network este neîncredere; numai Nginx/80 poate fi public.

## Filesystem

- `lstat`, nu follow implicit;
- refuz symlink pentru config, sites, journal, lock și custom snippets;
- open cu no-follow/create-exclusive unde API-ul Linux permite;
- verifică UID și tipul fiecărei componente managed;
- detectează path replacement între validare și read prin file descriptor/stat identity;
- atomic write + fsync file/director;
- umask restrictiv;
- limite de mărime/număr fișiere pentru a evita resource exhaustion;
- backup/journal nu devin executable și nu sunt world-readable.

Pentru proiecte, NixCP nu rulează `chmod -R`. Dacă Nginx nu poate traversa/read static assets, comanda eșuează cu path-ul și o recomandare precisă; utilizatorul decide permisiunile.

Paths sub directoare world-writable sunt refuzate sau cer un opt-in explicit viitor; v1 preferă refuz.

## Validare și escaping

### YAML

- strict unknown fields, duplicate-key rejection;
- limits pentru nesting/scalars;
- schema version mandatory;
- normalizare înainte de comparație/unicitate.

### Nix

- encoder dedicat pentru strings și paths;
- nu se interpolează expresii brute din YAML;
- snapshots custom sunt string-uri escaped, nu `import`/`builtins.readFile` din path necontrolat;
- golden/fuzz tests pentru `${`, quotes, newline, Unicode și comment markers.

### Nginx

- Go deține server context;
- parser/context validation + test Nginx real;
- denylist explicit pentru `ssl`, certificate, ACME, `listen`, `server_name`, context blocks și include arbitrar;
- domeniul nu conține wildcard/port/control chars;
- Host health requests folosesc valoare normalizată.

### Shell

- numai valorile din allowlist/path-uri construite intern sunt emise;
- quoting separat bash/zsh/fish;
- wrapper-ul eval-uiește numai subcomanda exactă și numai la succes;
- stdout shell-emit nu conține styling/progress;
- fuzz/injection tests cu spaces, quotes, substitutions și newlines.

### Procese privilegiate

- argv explicit, fără `sh -c`;
- rebuild mode/target validate și mapate la args;
- `systemctl` unit names sunt constante/derivate restrâns;
- binarul refuză EUID 0 și environment de sudo;
- nu acceptă command override prin YAML/environment;
- PATH pentru procese privilegiate este controlat ori se folosesc paths descoperite/verificate.

## Network și servicii

- Nginx ascultă numai HTTP/80 conform scopului;
- MariaDB ascultă unix socket/loopback, fără firewall opening;
- Redis ascultă loopback, protected mode;
- health-check confirmă listeners reali;
- nicio opțiune YAML nu configurează public bind pentru DB/cache;
- FPM folosește Unix sockets, nu TCP public;
- socket mode/group limitează accesul la Nginx și owner.

## Separarea site-urilor

Pool separat reduce interferența, dar toate pool-urile rulează ca același owner în model single-user; aceasta nu este izolare hostile multi-tenant. Documentația trebuie să spună explicit că NixCP v1 este pentru proiectele aceluiași utilizator de încredere, nu hosting pentru clienți neîncrezători.

- socket-uri separate și nume deterministe;
- Nginx vhost pointează numai la socket-ul site-ului;
- root/path canonic;
- fără include cross-site implicit;
- logs separate per site unde este practic.

## Invariantă „fără TLS pentru totdeauna”

Se aplică în fiecare layer:

- schema nu are câmpuri TLS;
- custom snippets cu termeni/directive TLS sunt respinse;
- renderer-ul emite numai `listen 80` fără `ssl`;
- nu există dependency ACME/certificate;
- tests negative blochează regresii;
- cererile viitoare de TLS necesită alt produs/fork, nu un flag ascuns NixCP.

## Secrets

V1 evită generarea parolelor DB prin Unix socket auth. Dacă un viitor feature cere secrets, acesta are nevoie de design separat; nu se pun în YAML, argv, logs sau generated world-readable files. `.env` nu este citit/modificat.

## Audit și diagnostics

- journal-ul păstrează acțiuni/hash-uri, nu conținut sensibil inutil;
- output JSON nu include automat stdout complet de servicii;
- diagnostics au limită de bytes și redaction hooks;
- `doctor` raportează permisiuni și listeners fără a face reparații.

## Testare securitate

- fuzz decoder YAML, normalizatori și encodere;
- symlink swap/owner mismatch tests;
- Nix injection corpus;
- Nginx context escape corpus;
- shell injection în toate cele 3 shell-uri;
- argv capture tests pentru sudo/rebuild/systemctl;
- VM test listeners (`ss`) pentru DB/Redis/FPM;
- negative tests TLS la schema și snippet;
- static analysis (`go vet`, `staticcheck`, govulncheck) și dependency scanning;
- race detector pentru state/transaction tests.

## Criterii de acceptanță

- niciun input nu ajunge ca expresie Nix/shell/SQL brută;
- symlink/owner/perms nesigure blochează înainte de sudo;
- MariaDB/Redis/FPM nu sunt publice;
- custom Nginx nu poate prelua server context ori activa TLS;
- NixCP nu rulează ca root;
- threat model-ul și limitarea non-multi-tenant sunt documentate;
- suitele fuzz/negative/race sunt verzi.
