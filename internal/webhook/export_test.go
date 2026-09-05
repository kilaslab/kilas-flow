package webhook

import (
	"net/http"

	"github.com/kilaslabs/kilas-flow/internal/repository"
)

// ReadDeliveryForTest exposes delivery capture so a shape test can build one
// from a real *http.Request rather than by hand, which is what makes the
// raw-body assertions meaningful.
func ReadDeliveryForTest(handler *Handler, r *http.Request, binding repository.WebhookBinding) (Delivery, error) {
	return handler.readDelivery(r, binding)
}
