# Hyllis

Dette er en instruksjonsfil for AI-agenter som jobber i dette repoet. Les den før du gjør endringer.

## Hva prosjektet er

En app for å holde oversikt over fysiske bøker man eier hjemme. Brukeren skanner ISBN-strekkoden med kameraet, appen slår opp bokmetadata automatisk, og boka legges i brukerens personlige bibliotek.
Man søker/filtrerer senere i eget bibliotek. Støtter norske og engelske bøker. Flerbruker – hver bruker har sitt eget bibliotek.

## Arkitektur

- **To tjenester, én binær:** systemet er delt i et **JSON-API** og en **web-app**, som deployes som to separate Fly-apper fra samme Docker-image. Ett entrypoint: `main.go` i repo-roten, modus velges med første argument (`run-app api` / `run-app web`; uten argument kjøres begge i samme prosess for lokal utvikling og `docker run`). Legg ikke en ny `cmd/server/main.go` tilbake – den er fjernet med vilje for å unngå duplikat-drift
  - **API** (`internal/apiserver`, Fly-app `hyllis-api`, `fly.api.toml`): eier Postgres, Redis og de eksterne ISBN-oppslagene, og all forretningslogikk (eierskap, duplikat-regler, søk/sortering/paging, validering). Kun privat via Flycast (`http://hyllis-api.flycast`), ingen offentlige IP-er. Alle `/v1`-kall krever `Authorization: Bearer <Supabase access token>`, som API-et verifiserer selv mot Supabase JWKS – det stoler ikke på klienten
  - **Web-app** (`internal/webapp`, Fly-app `hyllis`, `fly.toml`): HTMX-sider, statiske filer og Supabase-innloggingen (cookies). Eier **ingen data** og har ingen DB-/Redis-/Google Books-credentials; alt går via `internal/api.Client` mot `API_URL`, med brukerens eget access token videresendt
  - **Kontrakt** (`internal/api`): request/response-typer, feilkoder og klient. Web-appen skal aldri importere `internal/db`, `internal/lookup` eller repo-implementasjonene – kun `internal/api`
- **Frontend:** Go + HTMX (server-rendret HTML, ingen separat JS-frontend-rammeverk), kamera i nettleser for skanning
- **Database:** PostgreSQL (Supabase), aksessert via **Bun ORM** (`github.com/uptrace/bun` + `pgdialect`) i `internal/db`, over `pgx/v5`s `database/sql`-adapter. Poolen kjøres med `pgx.QueryExecModeSimpleProtocol` – kreves fordi Supabase sin pooler (PgBouncer, transaction mode) ikke støtter prepared statements. Domenetyper (`book.Book`, `library.Entry`, `user.User`) er fri for ORM-tags; hver repo-fil har egne private `*Model`-structs med `bun:"..."`-tags og mapping til/fra domenetypen
- **Migrasjoner:** `bun/migrate`, filene ligger i `migrations/*.sql` og embedes i binæren via `//go:embed` (`migrations/migrations.go`) – nødvendig fordi distroless-sluttlaget i Dockerfilen kun kopierer den kompilerte binæren, ikke `migrations/`-mappen. `internal/db.RunMigrations(ctx, db)` kjører alle uapplied migrasjoner og er trygg å kalle på hver oppstart (no-op når alt er applied)
- **Auth:** Supabase Auth
- **Cache:** Redis (Fly Redis / Upstash) – kun for eksterne ISBN-oppslag, ikke for brukerdata

To klart adskilte datalag:

- Postgres = sannheten om "hvem eier hvilken bok"
- Redis = cache av "hva betyr denne ISBN-en" (delt på tvers av alle brukere, praktisk talt immutabel data)

## Autentisering

Bruk **Supabase Auth** for registrering/innlogging – ikke bygg egen JWT-løsning i Go. Go-backend validerer Supabase sine access tokens (JWT) på beskyttede endepunkter, men eier ikke selve auth-flyten.

**Implementasjonsstatus:** web-appen (`internal/webapp`) gjør login/register/logout mot Supabase (`internal/auth.Client`) og holder sesjonen i HttpOnly-cookies (`auth.Middleware` verifiserer og refresher). Access tokenet videresendes som Bearer til API-et, som verifiserer det på nytt (`apiserver.requireBearer`) og scoper alt til `sub`. Profilspeilet i `users` oppdateres via `PUT /v1/me` etter hver innlogging.

## Regler for bokoppslag

Ved skanning av ny ISBN, følg denne rekkefølgen strengt:

1. Sjekk Redis-cache (`isbn:<isbn13>`) først – dette skjer alltid sekvensielt og først, aldri parallelt med noe annet
2. Ved miss: spør **Google Books API**, **Open Library API**, **Nasjonalbibliotekets søke-API** (`nb.no/services/search/v2`) og **bibliotekenes.no** (`bibliotekenes.no/api/e-commerce/search/bibsent-suggestions?query=<isbn13>`, Biblioteksentralens produktsøk) **parallelt** (goroutines) – ikke sekvensielt. Prioritetsrekkefølgen Google Books > Open Library > NB > bibliotekenes.no er likevel bindende for *hvilket svar som vinner*: når alle har svart, velges første treff i den rekkefølgen uansett hvilken goroutine som faktisk fullførte først. Dette er implementert i `internal/lookup.Service.Resolve` (kalt fra API-ets `GET /v1/lookup/{isbn}`) – endre ikke til strengt sekvensielt uten å diskutere med bruker, det var en explisitt ytelses-motivert endring
3. Lagre resultatet i Redis før det returneres

### Hvis alle fire API-ene bommer
Hvis Google Books, Open Library, NB og bibliotekenes.no alle ikke finner ISBN-en, skal ikke oppslaget feile stille. Brukeren skal få mulighet til å legge inn tittel, forfatter og evt. forlag manuelt, og boka lagres med `kilde: manual` i stedet for en av API-kildene.

## Datamodell

Relasjonell (Postgres), ikke dokument-DB – data skal filtreres/sorteres på kombinasjoner av felt.

- `users` – autentiserte brukere (styres av Supabase Auth)
- `books` – delt bok-metadata (isbn, tittel, forfatter, forlag, år, omslags-URL, kilde). Én rad per ISBN, delt mellom alle brukere. `source`/kilde-kolonnen er begrenset til `google_books | open_library | nb | bibliotekenes | manual` via check constraint (migrasjon `000005`, utvidet med `bibliotekenes` i `000006`)
- `user_books` – kobling bruker↔bok: eierskap, lagt-til-dato, lesestatus (`unread|reading|read`, migrasjon `000008`), evt. notat/plassering (tabellen heter `library_entries`, migrasjon `000004`; redigeres fra bokdetaljsiden via `PATCH /books/:id`)

Aldri dupliser bok-metadata per bruker – slå opp/opprett i `books`, koble via `user_books`.

**Migrasjoner ved oppstart:** API-et (`main.go` → `newDB`) kjører `db.RunMigrations` hver gang det starter med `DATABASE_URL` satt. Web-appen rører aldri databasen.

### Duplikat-regler
- Unik constraint på `books.isbn` – én rad per ISBN, uansett hvor mange brukere som eier boka
- Unik constraint på `(user_id, book_id)` i `user_books` – forhindrer at samme bok legges til flere ganger for samme bruker ved dobbel-scan eller dobbelttrykk
- Ved forsøk på å legge til en bok brukeren allerede har: returner eksisterende rad i stedet for å feile eller opprette duplikat

## Endepunkter

### JSON-API (`internal/apiserver`, alle krever Bearer-token)

- `GET /v1/lookup/{isbn}` – cache → eksterne API-er, **lagrer ingenting**. 404 `not_found` når alle kilder bommer. **Eneste endepunkt som snakker med eksterne tjenester**
- `GET /v1/library` – søk/filtrer/sorter/paginer eget bibliotek (`q`, `field`, `sort`, `page`, `per_page`; `per_page=-1` = alt, brukes av CSV-eksport). Rent lokalt mot Postgres
- `POST /v1/library` – legg til bok (201), eller returner eksisterende rad med `existed: true` (200) ved duplikat
- `POST /v1/library/import` – batch-import (maks 5000), kun data fra fila, ingen oppslag
- `GET /v1/library/isbn/{isbn}` – brukerens eksemplar av en ISBN (404 hvis ikke eid)
- `GET|PATCH|DELETE /v1/library/{id}` – `id` er library entry-id, aldri `books.id`. Andres oppføringer gir 404
- `GET|PUT /v1/me` – profilspeil; id/e-post tas alltid fra tokenet
- Feil er `{"code": "...", "message": "..."}` med kodene i `internal/api` – web-appen mapper koder til norske meldinger

### Web-app (`internal/webapp`, HTML/HTMX, ikke JSON)

- `POST /books/scan` – ISBN fra kamera/manuelt felt → `GET /v1/lookup`; preview, eller lagring ved "auto". Ved `not_found` rendres manuelt utfyllingsskjema
- `POST /books/confirm`, `POST /books/manual` (`kilde: manual`) → `POST /v1/library`
- `GET /library` (alias `GET /books`), `GET /books/search` (htmx-partial) → `GET /v1/library`
- `GET|PATCH|DELETE /books/{id}` → tilsvarende `/v1/library/{id}`; DELETE svarer 204 + `HX-Redirect: /library` ved htmx-kall
- `GET /library/export.csv`, `POST /library/import` – CSV-formatet håndteres i web-appen, data via API-et

Autentisering skjer via Supabase Auth (se eget avsnitt) – ingen egne `/auth/*`-endepunkter i Go-backend.

Viktig prinsipp: lookup er den eneste operasjonen som snakker med eksterne tjenester. Alt søk i egen bokhylle skal være rent lokalt mot Postgres og dermed alltid raskt, uavhengig av bibliotekstørrelse.

## Søk og filtrering i eget bibliotek

- Søket kjøres i API-et (`apiserver.pageOf`), ikke i web-appen.
- Søk er **fuzzy/skrivefeil-tolerant** (`internal/fuzzy`, avhengighetsfri – substring-match per ord med Levenshtein-fallback), ikke eksakt substring-match. Matcher på tittel + forfatter.
- Feltvelgeren i biblioteket ("Vis kun bøker med …") er **rent kosmetisk utvisning** – den påvirker aldri hva søket matcher på, bare hvilke felt som vises i resultatlisten. Søk og filter er to uavhengige ting; kobl dem ikke sammen igjen.
- Resultater sorteres deterministisk (nyeste `created_at` først, ISBN som tiebreak) – viktig i minne-repoet siden Go's map-iterasjon ellers gir tilfeldig rekkefølge.

## Frontend-regler

- **HTMX + Go**: server-rendrer HTML-fragmenter fra Go, HTMX håndterer partial page updates. Ikke introduser React/Vue eller lignende SPA-rammeverk.
- Strekkodeskanning: `BarcodeDetector` (nettleser-API) som primær, `zxing-js` som fallback for nettlesere uten native støtte. Skanning skjer client-side i vanlig JS (dette er det ene unntaket fra "ingen egen JS-frontend" – strekkodelesing krever kamera-tilgang i nettleseren)
- ISBN-strekkoden er standard EAN-13 – ikke bygg egen bok-spesifikk dekodingslogikk
- Vis lasteindikator ved skann/legg-til (kan ta et par hundre ms ved cache-miss mot eksterne API-er) – bruk HTMX sin innebygde loading-indicator-støtte (`hx-indicator`)
- Kamera krever HTTPS

## Miljøvariabler

Secrets i Fly.io, fordelt på de to appene:

- **`hyllis-api`:** `DATABASE_URL` (Supabase Postgres), `REDIS_URL` (Fly Redis/Upstash), `SUPABASE_URL` (JWKS for token-verifisering), `GOOGLE_BOOKS_API_KEY` (server-side kun)
- **`hyllis` (web):** `SUPABASE_URL` / `SUPABASE_ANON_KEY` (login/refresh/JWKS). `API_URL` står som vanlig env i `fly.toml`. Web-appen skal **ikke** ha `DATABASE_URL`, `REDIS_URL`, `SUPABASE_SERVICE_ROLE_KEY` eller `GOOGLE_BOOKS_API_KEY` – de bør fjernes derfra (`fly secrets unset -a hyllis ...`) når API-appen er oppe
- `PORT` (web 8080, API 8081), og i kombinert lokal modus `API_PORT` (default 8081, kun loopback)

### Lokal utvikling

`main.go` kaller `godotenv.Load()` først i `main()` for å lese en lokal `.env`-fil – rent utviklingsverktøy, feiler stille (ignorert error) hvis filen ikke finnes, så Docker/Fly (som injiserer ekte env-vars) er upåvirket. Uten argument kjører `go run .` både API (127.0.0.1:8081) og web (8080) i samme prosess. Mangler `DATABASE_URL`/`REDIS_URL` lokalt, faller API-et tilbake til hhv. en in-memory bok-repo og `lookup.NoopCache` i stedet for å feile ved oppstart.

**⚠️ Sikkerhet:** `.env` skal aldri committes med ekte secrets. Sjekk `git status`/`git ls-files` før du rører denne filen – den har historisk vært git-tracked med ekte Redis- og Supabase-credentials i working tree. Hvis den er tracked: foreslå `git rm --cached .env` + commit, og flagg for bruker at eksponerte credentials bør roteres. Gjør ikke dette selv uten explisitt bekreftelse fra bruker – det er destruktivt/uigjenkallelig nok til å kreve godkjenning.

## Driftsnotater

- Deploy (`.github/workflows/fly-deploy.yml`) kjører API-et først (`fly.api.toml`, `--no-public-ips`), så web-appen (`fly.toml`)
- Ingen av appene har lokal persistent state – all data i Postgres/Redis, så appen kan restartes/skaleres fritt
- Ingen Fly Volume nødvendig
- `DATABASE_URL` peker til ekstern Supabase-instans