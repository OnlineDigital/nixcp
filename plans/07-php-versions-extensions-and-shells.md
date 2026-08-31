# Etapa 07 — PHP, extensii și integrarea shell

## Obiectiv și dependențe

Coexistența mai multor versiuni PHP CLI/FPM, extensii nixpkgs globale și selecție locală/de sesiune care permite terminale paralele cu versiuni diferite.

**Depinde de:** 03–05; pool-urile se integrează cu etapa 08.

## Catalog versiuni

Un catalog Go versionat mapează versiuni normalizate la atribute nixpkgs (`8.3` → `php83`, `8.4` → `php84`) și capabilități. Catalogul este verificat la evaluare; indisponibilitatea în nixpkgs produce eroare acționabilă. Nu există fallback la surse externe.

Version policy v1:

- input exact `major.minor`;
- numai versiuni prezente în allowlist și nixpkgs țintă;
- patch level este cel din nixpkgs;
- instalarea este declarativă/idempotentă;
- eliminarea versiunilor nu este în v1.

## Extensii

Starea păstrează lista globală dorită:

```yaml
php:
  installed: ["8.3", "8.4"]
  extensions: [curl, intl, mbstring, opcache, pdo_mysql, redis]
  globalDefault: "8.4"
```

Resolver-ul inspectează extension set-ul fiecărei versiuni. Exemple precum `redis` și `opcache` sunt acceptate numai dacă există efectiv în nixpkgs pentru acea versiune/revizie.

Algoritm `php ext install X`:

1. normalizează și validează numele;
2. rezolvă atributul/aliasul canonic;
3. verifică toate versiunile instalate;
4. dacă nu este disponibil nicăieri, eșuează (probabil typo);
5. adaugă extensia o singură dată în desired state;
6. compune PHP separat per versiune;
7. omite combinațiile incompatibile și produce warnings structurate;
8. build/switch cu extensiile compatibile.

La `php install 8.4`, aceeași listă globală este aplicată automat. CLI și FPM folosesc aceeași compoziție. Warnings includ `extension`, `phpVersion`, `reason`; nu cauzează exit nonzero.

Nu există PECL, compilare arbitrară sau download extern.

## Căi PHP stabile

Renderer-ul expune:

```text
/etc/nixcp/php/8.3/bin/php
/etc/nixcp/php/8.4/bin/php
```

Calea reală pointează în Nix store la package composition. Nu se pun simultan mai multe `php` conflictuale în system PATH. Shell integration prepend-uiește directorul unei singure versiuni.

## `.php-version`

Conținut valid:

```text
8.4\n
```

- exact o versiune normalizată și whitespace final permis strict;
- extra tokens/conținut sunt eroare;
- write atomic în cwd pentru `ncp php use VERSION`;
- resolver-ul caută din cwd spre filesystem root;
- fișiere unreadable/invalide produc eroare cu path, nu sunt ignorate în tăcere;
- versiunea trebuie să fie instalată.

## De ce este necesar wrapper-ul

Procesul `ncp` nu poate schimba environment-ul shell-ului părinte. Pentru cerința ca imediat după:

```text
ncp php use 8.4
php -v
```

`php` să fie 8.4, bash/zsh/fish trebuie să definească o funcție `ncp` care interceptează cazul local `php use` și evaluează output-ul sigur al binarului.

## Protocol binar–shell

Comanda internă/publică controlată poate fi:

```text
command ncp php use 8.4 --shell-emit=bash
```

În modul shell-emit:

- validează și scrie `.php-version`;
- stdout conține **numai** cod shell;
- diagnostics merg pe stderr;
- codul se evaluează numai dacă exit code este 0;
- argumentul shell vine din allowlist bash/zsh/fish;
- valorile sunt quoted cu encoder specific shell-ului.

Codul rezultat:

1. elimină din PATH segmentul stocat anterior în `NIXCP_PHP_BIN`;
2. setează `NIXCP_PHP_VERSION=8.4`;
3. setează `NIXCP_PHP_BIN=/etc/nixcp/php/8.4/bin`;
4. prepend-uiește segmentul exact o dată;
5. exportă variabilele.

Nu se evaluează output arbitrar în alte comenzi.

## Wrapper bash/zsh conceptual

```sh
ncp() {
  if [ "$#" -eq 3 ] && [ "$1" = php ] && [ "$2" = use ] && /* local version */; then
    local code
    code="$(command ncp "$@" --shell-emit=bash)" || return $?
    eval "$code"
  else
    command ncp "$@"
  fi
}
```

Implementarea finală trebuie să trateze forma exactă acceptată și să nu intercepteze `--global`, flags necunoscute sau argumente extra. Zsh primește output compatibil propriu. Fish folosește o funcție și `source` numai după succes. Nu se folosește preview-ul conceptual ca implementare copy/paste fără teste de quoting.

`ncp shell init <shell>` tipărește snippet-ul; `ncp install` scrie aceeași versiune în `~/.nixcp/shell`. User-ul adaugă manual o singură linie în startup file.

## Default global și sesiuni paralele

`ncp php use --global 8.4` modifică numai `config.yaml`. Shell init, la pornirea unei sesiuni noi, citește default-ul printr-un output data-safe al binarului și îl capturează în `NIXCP_PHP_VERSION/BIN`. Acest lucru este furnizat de subcomanda internă ascunsă `ncp php session --shell-emit=<shell>`, care emite codul de activare pentru versiunea activă (dacă e instalată) sau pentru `globalDefault` (dacă e instalat), altfel nimic și exit 0.

Un terminal existent nu este rescris când default-ul se schimbă. Exemplu:

- terminal A activează local 8.4 și păstrează 8.4;
- terminal B activează local 8.0/altă versiune instalată și o păstrează;
- schimbarea globală afectează terminalul C deschis ulterior;
- `.php-version` din proiect are prioritate pentru comenzile NixCP.

### Precedență activă

1. cel mai apropiat `.php-version`;
2. `NIXCP_PHP_VERSION` valid din sesiune;
3. `globalDefault` curent pentru execuțiile fără sesiune integrată;
4. eroare `no_active_php_version`.

Pentru direct `php`, PATH-ul sesiunii este decisiv. Hook-ul poate opțional sincroniza la `cd`, dar **nu este necesar în v1** și nu trebuie introdus implicit; `ncp php use` face activarea explicită.

## `ncp php` și `ncp artisan`

- resolver-ul selectează calea stabilă;
- argv este transmis fără shell;
- cwd și environment sunt păstrate, cu PATH versiune consistent;
- stdin/stdout/stderr sunt conectate pentru mod interactiv;
- semnalele sunt propagate;
- exit code PHP este returnat;
- `artisan` este exact `selected-php ./artisan args...`, după lstat/readability check;
- `composer` folosește același resolver PHP peste scriptul stabil `/etc/nixcp/composer/bin/composer` (sistem-own, instalat de modulul generat); argumentele sunt transmise verbatim, exit code-ul Composer este propagat, și este necesar când proiectul are dependențe Composer (install/require/...);
- funcționează și fără wrapper shell.

## Teste shell reale

Pentru bash, zsh și fish:

- init și wrapper delegation;
- `use` schimbă direct `php -v` în același shell;
- două procese shell simultane păstrează versiuni diferite;
- global default afectează sesiuni noi, nu existente;
- PATH nu acumulează duplicate după activări repetate;
- trecerea 8.3→8.4 elimină segmentul vechi;
- path de proiect cu spații/metacaractere;
- activare invalidă nu schimbă PATH și nu corupe `.php-version`;
- `--global` nu este eval-uit;
- injection payload în argumente nu este executat.

## Criterii de acceptanță

- PHP CLI/FPM multiple coexistă cu extensii identice unde sunt compatibile;
- `redis`/orice extensie este acceptată numai pe baza nixpkgs real;
- incompatibilitatea parțială este warning și build-ul continuă;
- direct `php -v` se schimbă în sesiunea curentă după `ncp php use` cu hook;
- sesiuni paralele rămân independente;
- execuția PHP/Artisan păstrează argv, TTY, signals și exit code.
