package handler

import (
	"io"
	"net/http"

	"backend/internal/config"
	"backend/internal/service"
	"backend/internal/storage"
	"github.com/gin-gonic/gin"
)

type ImageHandler struct {
	cfg         *config.Config
	imageAccess *service.ImageAccessService
	store       *storage.Client
}

func NewImageHandler(cfg *config.Config, imageAccess *service.ImageAccessService, store *storage.Client) *ImageHandler {
	return &ImageHandler{
		cfg:         cfg,
		imageAccess: imageAccess,
		store:       store,
	}
}

// Serve proxies the object from RustFS. Generated media URLs are intentionally
// directly downloadable by API clients, so this route does not require a web
// session cookie. The object store itself remains private and is never exposed
// to the client.
func (h *ImageHandler) Serve(c *gin.Context) {
	user := c.Param("user")
	name := c.Param("name")

	rel, err := h.imageAccess.Resolve(user, name)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid path"})
		return
	}

	// A thumbnail shares its original's visibility, and old images without a
	// stored thumb fall back to the original object.
	origRel := rel
	if service.IsThumbKey(rel) {
		origRel = service.OrigKey(rel)
	} else if service.IsLastFrameKey(rel) {
		origRel = service.LastFrameOrigKey(rel)
	}

	// API clients commonly download this URL from a different origin (or from
	// a desktop renderer). Keep media fetches unrestricted and explicitly mark
	// them as cross-origin resources; the path contains a random filename and
	// the RustFS endpoint remains private.
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Cross-Origin-Resource-Policy", "cross-origin")

	// Forward Range so the browser can seek within videos.
	resp, err := h.store.Get(c.Request.Context(), rel, c.GetHeader("Range"))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"detail": "failed to fetch object"})
		return
	}
	if resp.StatusCode == http.StatusNotFound && origRel != rel {
		resp.Body.Close()
		resp, err = h.store.Get(c.Request.Context(), origRel, c.GetHeader("Range"))
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"detail": "failed to fetch object"})
			return
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		c.JSON(http.StatusNotFound, gin.H{"detail": "not found"})
		return
	}
	for _, hdr := range []string{"Content-Type", "Content-Length", "Accept-Ranges", "Content-Range", "Last-Modified", "ETag", "Cache-Control"} {
		if v := resp.Header.Get(hdr); v != "" {
			c.Header(hdr, v)
		}
	}
	c.Status(resp.StatusCode)
	_, _ = io.Copy(c.Writer, resp.Body)
}

// readCookie is shared by the session-auth handlers in this package.
func readCookie(c *gin.Context, name string) string {
	v, err := c.Cookie(name)
	if err != nil {
		return ""
	}
	return v
}
