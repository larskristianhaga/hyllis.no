# Hyllis

Dette er en instruksjonsfil for AI-agenter som jobber i dette repoet. Les den før du gjør endringer.

## Hva prosjektet er

En app for å holde oversikt over fysiske bøker man eier hjemme. Brukeren skanner ISBN-strekkoden med kameraet, appen slår opp bokmetadata automatisk, og boka legges i brukerens personlige bibliotek.
Man søker/filtrerer senere i eget bibliotek. Støtter norske og engelske bøker. Flerbruker – hver bruker har sitt eget bibliotek.

## Arkitektur

- **Backend:** Go, hostet på Fly.io, eksponerer REST API
- **Frontend:** Go + HTMX (server-rendret HTML, ingen separat JS-frontend-rammeverk), kamera i nettleser for skanning
- **Database:** PostgreSQL (Supabase)
- **Auth:** Supabase Auth
- **Cache:** Redis (Fly Redis / Upstash) – kun for eksterne ISBN-oppslag, ikke for brukerdata

To klart adskilte datalag:

- Postgres = sannheten om "hvem eier hvilken bok"
- Redis = cache av "hva betyr denne ISBN-en" (delt på tvers av alle brukere, praktisk talt immutabel data)

## Autentisering

Bruk **Supabase Auth** for registrering/innlogging – ikke bygg egen JWT-løsning i Go. Go-backend validerer Supabase sine access tokens (JWT) på beskyttede endepunkter, men eier ikke selve auth-flyten.

## Regler for bokoppslag

Ved skanning av ny ISBN, følg denne rekkefølgen strengt:

1. Sjekk Redis-cache (`isbn:<isbn13>`) først
2. Ved miss: spør **Google Books API**
3. Ved fortsatt miss: **Open Library API**
4. Ved fortsatt miss: spør **Nasjonalbibliotekets søke-API** (`nb.no/services/search/v2`) – fallback for norske titler
5. Lagre resultatet i Redis før det returneres

### Hvis alle tre API-ene bommer
Hvis Google Books, Open Library og NB alle ikke finner ISBN-en, skal ikke oppslaget feile stille. Brukeren skal få mulighet til å legge inn tittel, forfatter og evt. forlag manuelt, og boka lagres med `kilde: manual` i stedet for en av API-kildene.

## Datamodell

Relasjonell (Postgres), ikke dokument-DB – data skal filtreres/sorteres på kombinasjoner av felt.

- `users` – autentiserte brukere (styres av Supabase Auth)
- `books` – delt bok-metadata (isbn, tittel, forfatter, forlag, år, omslags-URL, kilde). Én rad per ISBN, delt mellom alle brukere.
- `user_books` – kobling bruker↔bok: eierskap, lagt-til-dato, lesestatus, evt. notat/plassering

Aldri dupliser bok-metadata per bruker – slå opp/opprett i `books`, koble via `user_books`.

### Duplikat-regler
- Unik constraint på `books.isbn` – én rad per ISBN, uansett hvor mange brukere som eier boka
- Unik constraint på `(user_id, book_id)` i `user_books` – forhindrer at samme bok legges til flere ganger for samme bruker ved dobbel-scan eller dobbelttrykk
- Ved forsøk på å legge til en bok brukeren allerede har: returner eksisterende rad i stedet for å feile eller opprette duplikat

## API-endepunkter (retningslinje)

- `POST /books/scan` – tar ISBN, går via cache→eksterne API-er, legger til i innlogget brukers bibliotek
- `GET /books` – søk/filtrer i eget bibliotek, **kun mot Postgres**, aldri eksterne kall herfra
- `GET /books/:id` – hent metadata + brukerdata for én bok
- `DELETE /books/:id`

Autentisering skjer via Supabase Auth (se eget avsnitt) – ingen egne `/auth/*`-endepunkter i Go-backend.

Viktig prinsipp: skann-operasjonen er den eneste som snakker med eksterne tjenester. Alt søk i egen bokhylle skal være rent lokalt mot Postgres og dermed alltid raskt, uavhengig av bibliotekstørrelse.

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

## Driftsnotater

- Go-appen har ingen lokal persistent state – all data i Postgres/Redis, så appen kan restartes/skaleres fritt
- Ingen Fly Volume nødvendig
- `DATABASE_URL` peker til ekstern Supabase-instans