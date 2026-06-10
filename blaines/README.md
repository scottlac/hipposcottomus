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

Everything that needs real data is marked in `index.html`:

| What | Where | How |
|---|---|---|
| Shop name | `index.html` (title, header, hero, footer) | Search for "Blaine's Barbershop" and replace |
| Services & prices | `index.html`, the block marked `EDIT SERVICES HERE` | Each service is one `<li class="service">` block — copy/paste to add, delete to remove. Replace `$XX` prices and durations. |
| Photos | `img/` | Drop real photos in `img/` and update the `src` (and `alt`!) on the matching `<img>` tags. Gallery shots look best square-ish; portraits of Blaine/Mack look best in 4:5. Any size works — CSS crops to fit. |
| Bios (Blaine & Mack) | `index.html` | Draft copy is already written; edit in place and delete the `[Draft bio …]` placeholder notes. |
| Hours / address / phone | `index.html`, "Visit Us" section | Marked with `PLACEHOLDER` comments. Update the `tel:` link too. |
| Instagram | `index.html` footer | Replace the placeholder `href`. |
| Map | `index.html`, `.map-placeholder` div | Replace the whole div with a real embed or a static map image when ready. |

Booking: every "Book Now" button points at the Square page
(`https://nc-107285.square.site/`). If that URL ever changes, search-and-replace
it in `index.html` (it appears in the header, hero, services, visit, and footer).

## Deploy

Automatic: every push to `main` builds the image, applies `deploy/k8s.yaml`,
and rolls out the new version (see `.github/workflows/deploy.yml`). Merging a
content change is all it takes.

Manual, if ever needed:

```sh
# 1. Build and push the image (from this directory)
docker build -f deploy/Dockerfile -t registry.digitalocean.com/jordan-lake-registry/blaines-site:latest .
docker push registry.digitalocean.com/jordan-lake-registry/blaines-site:latest

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
