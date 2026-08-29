# Etapa 04 — instalarea și generarea modulului NixOS

## Obiectiv și dependențe

Bootstrap single-user, import manual o singură dată și renderer Nix determinist pentru toate resursele.

**Depinde de:** 01–03.  
**Deblochează:** tranzacțiile și activarea serviciilor.

## Fluxul `ncp install`

1. Refuză execuția ca root sau prin `sudo`.
2. Verifică `/etc/os-release`, NixOS, systemd și `x86_64-linux`.
3. Descoperă user, UID/GID, grup și home prin API-uri OS, nu din input nesigur.
4. Verifică `nix`, `nixos-rebuild`, `systemctl` și suportul de lock.
5. Creează layout-ul și permisiunile `~/.nixcp`.
6. Scrie `config.yaml` inițial.
7. Instalează preset-urile Nginx și integrarea shell generată.
8. Generează `generated/nixcp-module.nix` gol/minimal.
9. Afișează instrucțiunile exacte de import și shell source.
10. Marchează `importConfirmed` numai după o validare separată reușită.

Nu editează fișiere în `/etc/nixos`, repo-ul flake, `.bashrc`, `.zshrc` sau fish config.

## Import tradițional

În `configuration.nix`:

```nix
{
  imports = [
    /home/alice/.nixcp/generated/nixcp-module.nix
  ];
}
```

După editare, utilizatorul rulează o comandă documentată, de exemplu:

```text
ncp install --confirm-import
```

Aceasta evaluează/build-uiește configurația efectivă și verifică marker-ul modulului NixCP.

## Import prin flake

Importul unui path absolut aflat în afara sursei flake are implicații de puritate. V1 suportă explicit o țintă precum `.#hostname` și folosește `--impure` numai când strategia aleasă o cere. Documentația trebuie să explice că alternativa mai pură este integrarea path-ului în input/repo, dar NixCP nu editează flake-ul.

Config-ul stochează `mode`, `target` și `impure` ca date validate; ele nu sunt concatenate într-o comandă shell.

## Contractul modulului generat

- header „generated, do not edit” și schema/generator version;
- assertion pentru `system == "x86_64-linux"`;
- marker verificabil pentru confirmarea importului;
- expresie formatată și byte-identical pentru același snapshot;
- nu citește YAML la Nix evaluation time;
- nu citește dinamic snippet-uri custom la evaluation: Go validează și incorporează conținutul;
- toate string-urile/path-urile trec printr-un encoder Nix dedicat.

Renderer-ul produce o expresie completă, nu fragmente modificate cu regex.

## Resurse generate

### Pachete și PHP

- fiecare versiune PHP are package composition separat cu extensiile compatibile;
- CLI și FPM pentru aceeași versiune folosesc aceeași compoziție;
- căi stabile sunt expuse, de exemplu `/etc/nixcp/php/8.4/bin/php`, prin `environment.etc`/symlink-uri către store;
- versiunile conflictuale nu sunt toate adăugate direct în system PATH;
- extensiile absente sunt filtrate per versiune și reflectate ca warnings în preflight/rezultat.

### PHP-FPM

- un pool per site ID;
- user = owner NixCP;
- socket determinist sub `/run/nixcp/php-fpm/<site-id>.sock`;
- group/mode permit accesul Nginx, nu acces global;
- config pool derivat exclusiv din manifest validat.

### Nginx

- HTTP `listen 80` explicit;
- un virtual host per site enabled;
- Go controlează server name, root, logs, indices, security și upstream socket;
- presetul/custom snippet este inserat în boundary-ul server/location prevăzut;
- niciun câmp SSL/TLS și nicio opțiune care activează automat ACME.

### MariaDB și Redis

- packages/options NixOS doar când sunt installed;
- desired stopped păstrează pachetul/config/data, dar elimină activarea automată controlată;
- MariaDB declară bazele din site-uri fără drop automat;
- user provisioning local este declarativ sau printr-o unitate oneshot idempotentă restrânsă;
- Redis ascultă numai loopback și nu este public.

## Problema serviciilor „installed dar stopped”

Modulele NixOS standard pot cupla `enable` cu pornirea unității. Renderer-ul trebuie să modeleze explicit:

- configurația/pachetul rămân prezente când installed;
- boot activation urmează `desiredState`;
- stop persistent nu șterge date/config;
- unitățile și `wantedBy`/start policy sunt testate pe versiunea nixpkgs suportată.

Nu se presupune că aceeași opțiune Nix funcționează identic pentru toate serviciile; fiecare adapter are golden și VM tests.

## Candidate build

Preflight-ul trebuie să testeze **candidatul**, nu modulul vechi de la path-ul importat. Planul de implementare va alege și testa una dintre strategiile:

1. staging path + configurație wrapper temporară care importă candidatul în locul path-ului stabil; sau
2. publicare reversibilă înainte de `nixos-rebuild build`, cu lock, backup atomic și restaurare garantată.

Strategia 1 este preferată dacă poate reproduce fidel configurația host/flake. Dacă limitările flake o fac nesigură, strategia 2 devine parte din tranzacția formală a etapei 05. Nu este acceptabil un `nixos-rebuild build` care încă importă vechiul modul.

Preflight include:

- parse/format Nix;
- evaluation a configurației țintă;
- `nixos-rebuild build`;
- validare Nginx derivată în build/VM, nu printr-un config incomplet local;
- assertions și paths referențiate.

## Compatibilitate nixpkgs

Versiunile PHP și attribute mappings sunt într-un catalog intern testat față de nixpkgs țintă. NixCP nu promite că o versiune eliminată din nixpkgs poate fi instalată din surse externe. Eroarea explică indisponibilitatea.

## Pași de implementare

1. Platform/bootstrap checks și layout creation.
2. Templates shell/Nginx built-in embedded în binar.
3. Nix AST/builder minimal ori encoder controlat cu golden tests.
4. Renderer pentru modul gol și marker import.
5. Suport traditional/flake și confirm-import.
6. Renderere serviciu/PHP/FPM/site incremental.
7. Candidate evaluation harness.
8. Determinism, escaping și compatibility tests.

## Criterii de acceptanță

- modulul gol importat evaluează pe `x86_64-linux` fără servicii active;
- `ncp install` este sigur și idempotent;
- utilizatorul primește instrucțiuni exacte, fără editări automate;
- același snapshot produce exact aceiași bytes;
- candidatul, nu versiunea veche, este build-uit;
- input-ul YAML/snippet nu poate ieși din string/contextul Nix;
- traditional și flake au teste separate.
