package swagger

import "github.com/swaggo/swag"

const docTemplate = `{
  "swagger": "2.0",
  "info": {
    "title": "ffxiv-census API",
    "description": "Auto-generated documentation stub. Replace with swagger docs for real endpoints.",
    "version": "1.0"
  },
  "paths": {
    "/health": {
      "get": {
        "summary": "Health check",
        "description": "Returns application status",
        "produces": [
          "application/json"
        ],
        "responses": {
          "200": {
            "description": "OK"
          }
        }
      }
    },
    "/example": {
      "get": {
        "summary": "Example placeholder",
        "description": "Demonstrates how to wire handlers",
        "produces": [
          "text/plain"
        ],
        "responses": {
          "200": {
            "description": "OK"
          }
        }
      }
    }
  }
}`

type swaggerDoc struct{}

func (swaggerDoc) ReadDoc() string {
    return docTemplate
}

func init() {
    swag.Register(swag.Name, &swaggerDoc{})
}
