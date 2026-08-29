# Etapa 06 — serviciile de sistem

## Obiectiv și dependențe

Implementarea lifecycle-ului uniform pentru Nginx, MariaDB și Redis, păstrând configurația declarativă și datele la stop.

**Depinde de:** 03–05.  
**Deblochează:** site-uri și baze declarate de site.

## Model comun

```yaml
services:
  nginx:
    installed: true
    desiredState: running
```

Stări valide:

```text
not installed
installed + stopped
installed + running
```

Actual state este citit din systemd și nu se scrie în YAML. `status` prezintă matricea desired/actual și drift.

## Contract lifecycle

- `install`: setează installed/running, generează și face rebuild; idempotent.
- `start`: necesită installed; persistă running prin rebuild.
- `stop`: persistă stopped, păstrează package/config/data și oprește unitatea.
- `restart`: necesită installed și actual/desired running; invocă systemd, nu schimbă YAML.
- `status`: read-only; systemd state, health, desired state, drift.

Un crash runtime nu modifică desired state. `doctor` recomandă restart/rebuild, dar nu repară automat.

## Nginx

Responsabilități:

- Nginx system package și unit;
- HTTP port 80 exclusiv;
- vhost-uri generate din site-uri;
- user/group/socket access compatibil cu pool-urile FPM;
- config validation în candidate build;
- log paths stabile și permisiuni corecte.

`stop` cu site-uri enabled returnează warning `nginx_stopped_with_enabled_sites`, dar operația continuă. `start` verifică faptul că toate manifests și snippets sunt valide înainte de rebuild.

Health:

1. `systemctl is-active nginx` conform desired;
2. config-ul activ este cel construit;
3. bind numai pe HTTP configurat;
4. pentru diff de site, request local cu `Host` și răspuns determinist.

## MariaDB

- folosește modulul/pachetul MariaDB din nixpkgs, nu MySQL alternativ;
- bind local/unix socket, fără expunere publică implicită;
- datadir gestionat de serviciul NixOS și niciodată șters de NixCP;
- bazele menționate de site-uri sunt ensure/create idempotent;
- unlink/stop nu face DROP DATABASE și nu șterge useri;
- backup/restore de date nu intră în v1.

### Cont local

Preferință: un account MariaDB asociat owner-ului OS, autentificat prin Unix socket și cu privilegii numai pe DB-urile declarate. Dacă API-ul modulului NixOS nu poate exprima sigur user+grants, se generează o unitate systemd oneshot:

- idempotentă;
- rulează după MariaDB ready;
- SQL este generat din identificatori strict validați;
- nu stochează parole;
- nu execută SQL arbitrar din YAML;
- rezultatul este verificat.

Health: systemd active + ping local + verificarea non-destructivă a DB-urilor/grants declarate.

## Redis

Default de securitate:

- bind `127.0.0.1` și, dacă este disponibil, `::1`;
- protected mode activ;
- nicio adresă publică și niciun firewall opening generat;
- fără cluster/sentinel/remote administration în v1;
- persistența folosește default-ul NixOS ales și documentat; stop nu șterge date.

Health: systemd active și `PING` local. Config-ul invalid sau bind-ul public este eroare, nu warning.

## Installed + stopped

Pentru fiecare serviciu se cercetează opțiunile modulului NixOS concret. Planul nu presupune că `enable=false` păstrează totul. Renderer-ul trebuie să producă:

- package/config disponibile;
- unit definită dar fără boot target când desired stopped, ori override echivalent;
- oprire efectivă la switch;
- revenire la running persistentă la start;
- date păstrate în ambele tranziții.

Acest comportament primește VM test separat per serviciu și reboot.

## Extensibilitate internă

O interfață internă poate defini:

```text
Validate(state), RenderNix(state), Status(), Health(), Restart()
```

Registrul v1 este o allowlist statică: nginx, mariadb, redis. Nu există dynamic plugins, nume arbitrare ori configurare generică de systemd units.

## Pași de implementare

1. State machine și handlers comuni.
2. Status desired/actual/drift și JSON schema.
3. Nginx adapter/render/health.
4. MariaDB adapter/render/provisioning/health.
5. Redis adapter/render/health.
6. Aliasuri top-level fără logică duplicată.
7. Stop/start persistence și restart operațional.
8. Golden, integration și VM tests.

## Failure/recovery

- unit failure după switch declanșează rollback tranzacțional;
- restart failure nu schimbă YAML și raportează jurnal systemd bounded;
- stop Nginx nu este blocat de site-uri, dar warning-ul este persistent în rezultat;
- MariaDB/Redis unavailable la health-check declanșează rollback de configurație, fără manipularea datadir-ului;
- NixCP nu încearcă „repair” destructiv.

## Criterii de acceptanță

- toate cele 5 acțiuni funcționează pentru cele 3 servicii;
- aliasurile și forma `service` sunt identice semantic;
- stop rămâne stop după reboot și nu dezinstalează/șterge date;
- status detectează drift;
- MariaDB și Redis sunt local-only;
- health failure activează rollback sigur.
