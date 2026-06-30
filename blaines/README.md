# Blaine's Barbershop — blaines.shop

A plain static site (HTML + CSS + a little vanilla JS, no build step) served at
`https://blaines.shop/` by nginx in a container on Kubernetes. All links and
asset paths are relative, so the site is portable across hosts/prefixes.

## Local preview

```sh
python3 -m http.server 8000
# open http://localhost:8000/
```

## Swapping in real content

Real content status (services synced from the Square booking page on 2026-06-10):

| What | Where | Status |
|---|---|---|
| Services & prices | `index.html`, the block marked `EDIT SERVICES HERE` | **Real.** Grouped by category; each service is one `<li class="service">` row. Keep names matching the Square page so customers find the same service after clicking Book Now. |
| Hours / address / phone / email | `index.html` "Visit Us" + footer + JSON-LD in `<head>` | **Real.** If hours change, update the table, the footer line, and the `openingHoursSpecification` JSON-LD. |
| Map | `index.html`, `.map-embed` div | **Real** keyless Google Maps embed pointed at 837 Perry Rd. |
| Logo & portraits | `img/logo.png`, `img/blaine.jpg`, `img/kevin.jpg`, `img/perry.jpg`, `img/mack.jpg` | **Real.** Logo in the hero; barbers in About; Mack in his section. |
| Hero & gallery photos | `img/hero.svg`, `img/gallery-*.svg` | **Placeholders.** Drop real shop/cut photos in `img/` and update the `src` (and `alt`!) on the matching `<img>` tags. Any size works — CSS crops to fit. |
| Bios | `index.html` | Blaine's is **draft copy** (edit and delete the `[Draft bio …]` note). Kevin and Mack/Perry copy is in place. |
| Instagram | `index.html` footer | **Placeholder** `href` — swap for the real profile. |

Booking: every "Book Now" button points at the Square page
(`https://nc-107285.square.site/`). If that URL changes, search-and-replace it in
`index.html` (header, hero, services, visit, footer).

Cache busting: `index.html` references `css/style.css?v=N` and `js/main.js?v=N`.
Bump `N` whenever you change the CSS or JS so returning visitors skip their
cached copy. (HTML itself is served `no-cache`, so content edits show up
immediately.)

## Deploy

Automatic: every push to `main` builds the image, applies `deploy/k8s.yaml`, and
rolls out the new version (see `.github/workflows/deploy.yml`). Merging a content
change is all it takes.

Requires one repo secret: **`DIGITALOCEAN_ACCESS_TOKEN`** (a DO API token with
registry + Kubernetes access).

Manual, if ever needed:

```sh
docker build -f deploy/Dockerfile -t registry.digitalocean.com/jordan-lake-registry/blaines-site:latest .
docker push registry.digitalocean.com/jordan-lake-registry/blaines-site:latest
kubectl apply -f deploy/k8s.yaml
```

The container is nginx-unprivileged listening on 8080, serving the site at `/`.
`deploy/k8s.yaml` defines the Deployment, Service, and an Ingress for
`blaines.shop` (TLS via cert-manager `letsencrypt-prod`). See the comments in
that file for DNS prerequisites and how to add `www`.

A weekly **Registry Cleanup** workflow prunes old image tags and runs garbage
collection so the registry doesn't fill up.

## Structure

```
.
├── index.html          # the whole site (single page, anchor nav)
├── css/style.css       # all styles, design tokens at the top in :root
├── js/main.js          # mobile nav toggle + header shrink-on-scroll
├── img/                # logo, real photos, and SVG placeholders
├── deploy/             # Dockerfile, nginx config, Kubernetes manifests
└── .github/workflows/  # build & deploy, registry cleanup
```
