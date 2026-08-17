package http

import (
	"embed"
	"io/fs"
	"net/http"

	httpSwagger "github.com/swaggo/http-swagger"
)

//go:embed resource/swagger/*
var swaggerFiles embed.FS

const SwaggerJSONRoute = "/docs/swagger.json"

func RegisterSwagger(mux *http.ServeMux) {
	fileServer := swaggerFileServer()

	mux.Handle(SwaggerJSONRoute, http.StripPrefix("/docs/", fileServer))
	mux.Handle("/docs/swagger.yaml", http.StripPrefix("/docs/", fileServer))

	swaggerHandler := httpSwagger.Handler(
		httpSwagger.URL(SwaggerJSONRoute),
	)

	mux.Handle("/docs/", swaggerHandler)
	mux.Handle("/docs", http.RedirectHandler("/docs/", http.StatusMovedPermanently))
}

func swaggerFileServer() http.Handler {
	sub, err := fs.Sub(swaggerFiles, "resource/swagger")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(sub))
}
