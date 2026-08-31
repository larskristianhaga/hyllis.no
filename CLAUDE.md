# Hyllis

Dette er en instruksjonsfil for AI-agenter som jobber i dette repoet. Les den før du gjør endringer.

## Hva prosjektet er

En app for å holde oversikt over fysiske bøker man eier hjemme. Brukeren skanner ISBN-strekkoden med kameraet, appen slår opp bokmetadata automatisk, og boka legges i brukerens personlige bibliotek.
Man søker/filtrerer senere i eget bibliotek. Støtter norske og engelske bøker. Flerbruker – hver bruker har sitt eget bibliotek.

## Arkitektur

- **Backend:** Go, hostet på Fly.io, eksponerer REST API. Ett entrypoint: `main.go` i repo-roten (Dockerfilen bygger root-pakken – legg ikke en ny `cmd/server/main.go` tilbake, den er fjernet med vilje for å unngå duplikat-drift)
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

**Implementasjonsstatus:** `/login` og `/register` finnes som sider i `internal/server/handlers.go`, men er placeholder – `loginSubmit`/`registerSubmit` rendrer bare en "ikke tilgjengelig ennå"-melding, ingen faktisk Supabase Auth-kall er koblet på. Det finnes heller ingen JWT-valideringsmiddleware eller session-håndtering ennå. `internal/user` har typer/interfaces, ikke wiring. Dette – pluss `internal/library`s per-bruker eierskap (`user_books`) – er den største gjenstående biten av "flerbruker"-kravet.

## Regler for bokoppslag

Ved skanning av ny ISBN, følg denne rekkefølgen strengt:

1. Sjekk Redis-cache (`isbn:<isbn13>`) først – dette skjer alltid sekvensielt og først, aldri parallelt med noe annet
2. Ved miss: spør **Google Books API**, **Open Library API** og **Nasjonalbibliotekets søke-API** (`nb.no/services/search/v2`) **parallelt** (goroutines) – ikke sekvensielt. Prioritetsrekkefølgen Google Books > Open Library > NB er likevel bindende for *hvilket svar som vinner*: når alle har svart, velges første treff i den rekkefølgen uansett hvilken goroutine som faktisk fullførte først. Dette er implementert i `internal/lookup.Service.Resolve` – endre ikke til strengt sekvensielt uten å diskutere med bruker, det var en explisitt ytelses-motivert endring
3. Lagre resultatet i Redis før det returneres

### Hvis alle tre API-ene bommer
Hvis Google Books, Open Library og NB alle ikke finner ISBN-en, skal ikke oppslaget feile stille. Brukeren skal få mulighet til å legge inn tittel, forfatter og evt. forlag manuelt, og boka lagres med `kilde: manual` i stedet for en av API-kildene.

## Datamodell

Relasjonell (Postgres), ikke dokument-DB – data skal filtreres/sorteres på kombinasjoner av felt.

- `users` – autentiserte brukere (styres av Supabase Auth)
- `books` – delt bok-metadata (isbn, tittel, forfatter, forlag, år, omslags-URL, kilde). Én rad per ISBN, delt mellom alle brukere. `source`/kilde-kolonnen er begrenset til `google_books | open_library | nb | manual` via check constraint (migrasjon `000005`)
- `user_books` – kobling bruker↔bok: eierskap, lagt-til-dato, lesestatus, evt. notat/plassering (finnes som migrasjon `000004_create_library_entries_table`, men `internal/library`-pakken er ikke koblet inn i noen handler ennå – se auth-avsnittets implementasjonsstatus)

Aldri dupliser bok-metadata per bruker – slå opp/opprett i `books`, koble via `user_books`.

**Kjent gap:** migrasjonene kjøres i tester (mot testcontainers-Postgres) og har en `db.RunMigrations`-funksjon klar til bruk, men blir per nå ikke kalt fra `main.go` ved oppstart mot den ekte Supabase-databasen – tabellene finnes derfor ikke der før dette er koblet på. Sjekk om dette er fikset før du antar at Supabase-skjemaet er oppdatert.

### Duplikat-regler
- Unik constraint på `books.isbn` – én rad per ISBN, uansett hvor mange brukere som eier boka
- Unik constraint på `(user_id, book_id)` i `user_books` – forhindrer at samme bok legges til flere ganger for samme bruker ved dobbel-scan eller dobbelttrykk
- Ved forsøk på å legge til en bok brukeren allerede har: returner eksisterende rad i stedet for å feile eller opprette duplikat

## API-endepunkter (retningslinje)

- `POST /books/scan` – tar ISBN (fra kamera eller manuelt felt), går via cache→eksterne API-er, lagrer i `books`. **Implementert.** Ved `lookup.ErrNotFound` (alle kilder bommet) rendres et manuelt utfyllingsskjema i stedet for å feile
- `POST /books/manual` – lagrer bok fra det manuelle skjemaet med `kilde: manual`. **Implementert**
- `GET /books` – alias for `/library`-siden (samme handler); søk/filtrer i eget bibliotek, rent server-rendret HTML, **kun mot Postgres/minne-repo**, aldri eksterne kall herfra. **Implementert**
- `GET /books/search` – htmx-partial for søk/filtrering i biblioteket (fuzzy søk, se frontend-regler). **Implementert**
- `GET /books/:id` – hent metadata + brukerdata for én bok. **Implementert**
- `DELETE /books/:id` – **Implementert**, svarer 204 + `HX-Redirect: /library` ved htmx-kall

Merk: dette er en HTMX-app, ikke et JSON-API – alle disse endepunktene returnerer HTML-fragmenter/sider, ikke JSON.

Autentisering skjer via Supabase Auth (se eget avsnitt) – ingen egne `/auth/*`-endepunkter i Go-backend.

Viktig prinsipp: skann-operasjonen er den eneste som snakker med eksterne tjenester. Alt søk i egen bokhylle skal være rent lokalt mot Postgres og dermed alltid raskt, uavhengig av bibliotekstørrelse.

## Søk og filtrering i eget bibliotek

- Søk er **fuzzy/skrivefeil-tolerant** (`internal/fuzzy`, avhengighetsfri – substring-match per ord med Levenshtein-fallback), ikke eksakt substring-match. Matcher på tittel + forfatter.
- Feltvelgeren i biblioteket ("Vis kun bøker med …") er **rent kosmetisk utvisning** – den påvirker aldri hva søket matcher på, bare hvilke felt som vises i resultatlisten. Søk og filter er to uavhengige ting; kobl dem ikke sammen igjen.
- Resultater sorteres deterministisk (nyeste `created_at` først, ISBN som tiebreak) – viktig i minne-repoet siden Go's map-iterasjon ellers gir tilfeldig rekkefølge.

## Frontend-regler

- **HTMX + Go**: server-rendrer HTML-fragmenter fra Go, HTMX håndterer partial page updates. Ikke introduser React/Vue eller lignende SPA-rammeverk.
- Strekkodeskanning: `BarcodeDetector` (nettleser-API) som primær, `zxing-js` som fallback for nettlesere uten native støtte. Skanning skjer client-side i vanlig JS (dette er det ene unntaket fra "ingen egen JS-frontend" – strekkodelesing krever kamera-tilgang i nettleseren)
- ISBN-strekkoden er standard EAN-13 – ikke bygg egen bok-spesifikk dekodingslogikk
- Vis lasteindikator ved skann/legg-til (kan ta et par hundre ms ved cache-miss mot eksterne API-er) – bruk HTMX sin innebygde loading-indicator-støtte (`hx-indicator`)
- PWA: manifest + service worker for installerbarhet; kamera krever HTTPS

## Miljøvariabler

Forventede env-vars som ligger i som secrets i Fly.io:

- `DATABASE_URL` – tilkobling til Supabase Postgres
- `SUPABASE_URL` / `SUPABASE_ANON_KEY` / `SUPABASE_SERVICE_ROLE_KEY` – for auth-validering og evt. server-side Supabase-kall
- `REDIS_URL` – tilkobling til Fly Redis/Upstash
- `GOOGLE_BOOKS_API_KEY` – for Google Books-oppslag (server-side kun, aldri eksponert til frontend)

### Lokal utvikling

`main.go` kaller `godotenv.Load()` først i `main()` for å lese en lokal `.env`-fil – rent utviklingsverktøy, feiler stille (ignorert error) hvis filen ikke finnes, så Docker/Fly (som injiserer ekte env-vars) er upåvirket. Mangler `DATABASE_URL`/`REDIS_URL` lokalt, faller appen tilbake til hhv. en in-memory bok-repo og `lookup.NoopCache` i stedet for å feile ved oppstart.

**⚠️ Sikkerhet:** `.env` skal aldri committes med ekte secrets. Sjekk `git status`/`git ls-files` før du rører denne filen – den har historisk vært git-tracked med ekte Redis- og Supabase-credentials i working tree. Hvis den er tracked: foreslå `git rm --cached .env` + commit, og flagg for bruker at eksponerte credentials bør roteres. Gjør ikke dette selv uten explisitt bekreftelse fra bruker – det er destruktivt/uigjenkallelig nok til å kreve godkjenning.

## Driftsnotater

- Go-appen har ingen lokal persistent state – all data i Postgres/Redis, så appen kan restartes/skaleres fritt
- Ingen Fly Volume nødvendig
- `DATABASE_URL` peker til ekstern Supabase-instans