# hyllis.no

This repository contains the sourcecode for the website [hyllis.no](https://hyllis.no/)

A webpage for keeping track of physical books you own at home. The user scans the ISBN barcode with their camera, the app automatically looks up book metadata, and the book is added to the user's
personal library. The user can later search/filter in their own library. Supports Norwegian and English books. Multi-user – each user has their own library.

## Running locally

```bash
docker run -it --rm -p 8080:8080 $(docker build -q .)
```