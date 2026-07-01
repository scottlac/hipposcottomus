package main

import (
	"io/fs"
	"log"
	"net/http"
)

const qrBasePath = "/qr"

// InitQR registers the client-side QR code generator. It has no backend —
// everything (encoding, rendering, export) happens in the browser — so we
// only need to serve its embedded static assets.
func InitQR(mux *http.ServeMux) {
	staticSub, err := fs.Sub(content, "qr")
	if err != nil {
		log.Fatalf("[QR] failed to load embedded static files: %v", err)
	}
	fileServer := http.FileServer(http.FS(staticSub))
	mux.Handle(qrBasePath+"/", http.StripPrefix(qrBasePath, fileServer))

	log.Printf("[QR] QR code generator registered at %s/", qrBasePath)
}
