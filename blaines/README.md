# Blaine's Barbershop — v1 draft site

A plain static site (HTML + CSS + a little vanilla JS, no build step) served at
`https://hipposcottomus.com/blaines/`. All links and asset paths are relative,
so the site works at any URL prefix — including its own domain later.

## Local preview

From this directory:

```sh
python3 -m http.server 8000
# open http://localhost:8000/
```

To verify subpath behavior (how it'll look under `/blaines/`), serve the
**repo root** instead and browse to the subdirectory:

```sh
cd ..   # repo root
python3 -m http.server 8000
# open http://localhost:8000/blaines/
```

## Swapping in real content

Real content status (synced from the Square booking page on 2026-06-10):

| What | Where | Status |
|---|---|---|
| Services & prices | `index.html`, the block marked `EDIT SERVICES HERE` | **Real.** Mirrors the Square page — keep names matching so customers find the same service after clicking Book Now. Each service is one `<li class="service">` block. |
| Hours / address / phone / email | `index.html` "Visit Us" + footer + JSON-LD in `<head>` | **Real.** If hours change, update the table, the footer line, and the `openingHoursSpecification` JSON-LD. |
| Map | `index.html`, `.map-embed` div | **Real** keyless Google Maps embed pointed at 837 Perry Rd. |
| Logo & portraits | `img/logo.png`, `img/blaine.jpg`, `img/mack.jpg` | **Real.** Logo is in the hero (white background made transparent); portraits in About/Mack sections. |
| Hero & gallery photos | `img/hero.svg`, `img/gallery-*.svg` | **Placeholders.** Drop real shop/cut photos in `img/` and update the `src` (and `alt`!) on the matching `<img>` tags. Gallery shots look best square-ish. Any size works — CSS crops to fit. |
| Bios (Blaine & Mack) | `index.html` | **Draft copy** — edit in place and delete the `[Draft bio …]` placeholder notes. Kevin and Perry could use intros. |
| Instagram | `index.html` footer | **Placeholder** `href` — swap for the real profile. |

Booking: every "Book Now" button points at the Square page
(`https://nc-107285.square.site/`). If that URL ever changes, search-and-replace
it in `index.html` (it appears in the header, hero, services, visit, and footer).

## Deploy

Automatic: every push to `main` builds the image, applies `deploy/k8s.yaml`,
and rolls out the new version (see `.github/workflows/deploy.yml`). Merging a
content change is all it takes.

Manual, if ever needed:

```sh
# 1. Build and push the image (from this directory). The image lives in the
#    jordan-lake-scraper repository under blaines-* tags because the DO
#    registry Starter plan allows only one repository.
docker build -f deploy/Dockerfile -t registry.digitalocean.com/jordan-lake-registry/jordan-lake-scraper:blaines-latest .
docker push registry.digitalocean.com/jordan-lake-registry/jordan-lake-scraper:blaines-latest

# 2. Apply the manifests (Deployment + Service + Ingress)
kubectl apply -f deploy/k8s.yaml
```

`deploy/k8s.yaml` includes two ingress options (standalone Ingress vs. adding a
path to the existing one) plus notes for moving to a dedicated domain later —
see the comments in that file. The container is nginx-unprivileged listening on
8080, serving the site at `/blaines/`, matching the ingress path with no
rewrites.

## Structure

```
blaines/
├── index.html      # the whole site (single page, anchor nav)
├── css/style.css   # all styles, design tokens at the top in :root
├── js/main.js      # mobile nav toggle only
├── img/            # SVG placeholders — replace with real photos
└── deploy/         # Dockerfile + Kubernetes manifests
```
