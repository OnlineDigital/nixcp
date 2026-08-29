# Etapa 05 — tranzacții, lock și rebuild

## Obiectiv și dependențe

O singură cale sigură pentru toate mutațiile persistente, cu serializare, no-op detection, jurnal, build candidat, switch, health-check și rollback.

**Depinde de:** 02–04.  
**Blochează integrarea:** niciun feature persistent nu intră în producție înaintea acestei etape.

## Clasificarea operațiilor

1. **Read-only:** status/list/show/doctor; shared lock sau snapshot committed coerent.
2. **Persistent declarativ:** service install/start/stop, PHP install/ext/global use, link/unlink; tranzacție completă.
3. **Operațional:** restart; lock scurt, verificări și `systemctl restart`, fără YAML/rebuild.
4. **Local user-state:** `.php-version`; atomic write local, fără rebuild, dar cu protecții de path.

## Protocolul mutației persistente

1. Acquire exclusive lock cu context și timeout bounded.
2. Recovery check pentru tranzacții stale.
3. Load snapshot și verifică owner/symlinks/schema.
4. Aplică use-case-ul în memorie.
5. Validează snapshot-ul propus integral.
6. Calculează diff semantic; dacă este gol, returnează `changed:false`.
7. Render YAML/Nix candidat și warnings.
8. Scrie staging pe același filesystem, cu mod/owner corect.
9. Persistă journal phase `staged` și fsync.
10. Build/preflight pe candidatul real.
11. Salvează referința generației curente și backup-ul fișierelor.
12. Publică atomic YAML + modul conform protocolului ales.
13. `sudo nixos-rebuild switch` cu argv validat.
14. Health-check numai pe resursele afectate.
15. Marchează `committed`, fsync directoare și curăță staging.
16. Returnează diff și warnings.

„Atomic” pentru mai multe fișiere este obținut prin jurnal + publish order + recovery, nu prin falsa presupunere că mai multe rename-uri sunt o singură operație POSIX.

## Lock

- un lock stabil `~/.nixcp/lock`, compatibil `flock`;
- exclusive pentru mutații, shared pentru reads ce cer consistență strictă;
- lock-ul rămâne ținut până după health-check/rollback;
- mesajul de conflict include PID/transaction ID dacă este sigur;
- timeout configurabil într-o limită rezonabilă;
- lock-ul kernel se eliberează la crash; journal-ul rezolvă starea stale.

## Staging și publicare

- staging sub `~/.nixcp/transactions/<id>/` ca rename-ul să rămână pe același filesystem;
- toate fișierele sunt create cu create-exclusive/no-follow;
- bytes sunt fsync înainte de rename;
- directorul părinte este fsync după rename;
- backup-ul conține manifestele precedente și hash-uri;
- generated module și YAML au hash-uri în journal;
- template/custom source folosit la render este snapshot-uit pentru consistență.

## Journal

Exemplu conceptual:

```yaml
id: 2025...
phase: staged
oldGeneration: /nix/var/nix/profiles/system-123-link
oldHashes: {}
candidateHashes: {}
affectedResources: []
startedAt: ...
```

Faze: `created`, `staged`, `built`, `published`, `switched`, `verified`, `committed`, `rolling-back`, `rolled-back`, `rollback-failed`.

La startup, orice fază ne-terminală este inspectată. NixCP nu continuă cu o nouă mutație până când nu restaurează coerent ori oferă instrucțiuni exacte de intervenție.

## Matrice de eșec

| Eșec | Acțiune |
|---|---|
| înainte de staging | nimic de restaurat |
| staging/render | șterge staging; starea committed rămâne |
| candidate build | păstrează committed; atașează diagnostics |
| publish înainte de switch | restaurează fișierele vechi atomic |
| switch | restaurează YAML/modul și activează generația veche |
| health-check | rollback la generația și starea veche |
| rollback eșuat | exit 8, journal păstrat, instrucțiuni fără afirmație de succes |
| SIGINT/SIGTERM | anulează sigur; dacă publish a început, finalizează rollback |

Rollback-ul activ folosește referința generației anterioare ori rebuild-ul stării restaurate. Comanda exactă este testată pe NixOS. Rezultatul raportează separat eroarea inițială și eroarea de rollback.

## Sudo și procese privilegiate

- întreg binarul nu rulează sub sudo;
- allowlist: comenzile exacte `nixos-rebuild` și operații `systemctl` documentate;
- fără user-controlled flags arbitrare;
- prompt-ul sudo rămâne vizibil numai în human/TTY;
- în `--json --no-input`, lipsa unui credential cached produce eroare privilege predictibilă, nu prompt blocat;
- stdout/stderr sunt capturate bounded și redactate.

## Build versus switch

`build` trebuie să folosească exact snapshot-ul candidat. După succes, `switch` trebuie să activeze aceeași derivation/configurație; dacă toolchain-ul nu oferă direct această garanție, hash-ul/closure path este înregistrat și comparat. Nu se permite schimbarea concomitentă a fișierelor importate deoarece lock-ul NixCP le protejează.

## Health-check

Verificările sunt specifice diff-ului:

- service running: systemd active plus ping/config check;
- service stopped: inactive și fără auto-start nedorit;
- site: socket FPM existent, Nginx active și request local cu Host header;
- PHP paths: executabilul raportează versiunea corectă;
- link cu DB: database/user provisioning verificat non-destructiv.

Timeout-urile sunt finite. Un warning de compatibilitate extensie nu este health failure.

## Teste fault-injection

Injectează eșec după fiecare pas: write, fsync, build, publish N, switch, verify, restore, rollback. Verifică:

- starea committed rămâne validă;
- modulul și YAML nu diverg tăcut;
- journal permite recovery după kill -9;
- două mutații nu rulează în paralel;
- no-op nu cere sudo/rebuild;
- Ctrl-C înainte/după publish are rezultat determinist;
- output JSON include phases relevante fără ANSI.

## Criterii de acceptanță

- toate use-case-urile persistente folosesc același transaction manager;
- build-ul candidat se întâmplă înainte de activare;
- rollback este demonstrat prin fault tests și NixOS VM;
- niciun eșec nu raportează succes dacă active state diferă;
- lock contention și stale recovery sunt acționabile;
- no-op nu rulează rebuild.
