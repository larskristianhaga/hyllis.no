# hyllis.no

This repository contains the sourcecode for the website [hyllis.no](https://hyllis.no/)

A webpage for keeping track of physical books you own at home. The user scans the ISBN barcode with their camera, the app automatically looks up book metadata, and the book is added to the user's
personal library. The user can later search/filter in their own library. Supports Norwegian and English books. Multi-user – each user has their own library.

## Architecture

Two services built from one binary/image:

- **API** (`run-app api`, Fly app `hyllis-api`, [fly.api.toml](fly.api.toml)) – JSON API that owns Postgres, Redis and the external ISBN lookups. Private (Flycast only); every `/v1` request is authenticated with the end user's Supabase access token.
- **Web app** (`run-app web`, Fly app `hyllis`, [fly.toml](fly.toml)) – HTMX pages and the Supabase login flow. Holds no data or data credentials; calls the API at `API_URL`.

## Running locally

Without arguments the binary runs both services in one process (API on `127.0.0.1:8081`, web on `:8080`):

```bash
docker run -it --rm -p 8080:8080 $(docker build -q .)
```

## First-time setup of the API app on Fly

```bash
fly apps create hyllis-api
fly ips allocate-v6 --private -a hyllis-api
fly secrets set -a hyllis-api DATABASE_URL=... REDIS_URL=... SUPABASE_URL=... GOOGLE_BOOKS_API_KEY=...
fly deploy -c fly.api.toml --no-public-ips
fly deploy -c fly.toml
fly secrets unset -a hyllis DATABASE_URL REDIS_URL GOOGLE_BOOKS_API_KEY SUPABASE_SERVICE_ROLE_KEY
```