package api

import (
	"context"
	"testing"
)

func TestOpenAPISpec(t *testing.T) {
	t.Parallel()

	spec, err := GetSwagger()
	if err != nil {
		t.Fatalf("GetSwagger() error = %v", err)
	}
	if err := spec.Validate(context.Background()); err != nil {
		t.Fatalf("validate OpenAPI spec: %v", err)
	}
}
