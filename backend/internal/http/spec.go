package http

import (
	"fmt"
	"os"

	"github.com/danielgtaylor/huma/v2"

	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// Spec prints the OpenAPI document and says whether that was all this run was
// for. It needs only the routes, so it runs on a checkout with no stack.
func Spec(args []string, service string, mount func(huma.API)) bool {
	if len(args) < 2 || args[1] != "openapi" {
		return false
	}

	_, api := New(service, "dev", applog.New(service))
	mount(api)

	out, err := api.OpenAPI().YAML()
	if err != nil {
		fmt.Fprintln(os.Stderr, service+": openapi:", err)
		os.Exit(1)
	}
	fmt.Println(string(out))
	return true
}
