// SPDX-License-Identifier: Apache-2.0
// Command sample-client demonstrates authenticated tenant discovery without an SDK package.
package main

import (
	"connectrpc.com/connect"
	"context"
	"flag"
	"fmt"
	pb "github.com/delinoio/oss/protos/gen/go/delcore/v1"
	"github.com/delinoio/oss/protos/gen/go/delcore/v1/delcorev1connect"
	"net/http"
	"os"
	"time"
)

func main() {
	endpoint := flag.String("endpoint", "http://127.0.0.1:8080", "delcore API endpoint")
	flag.Parse()
	token := os.Getenv("DELCORE_ACCESS_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "DELCORE_ACCESS_TOKEN is required")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := delcorev1connect.NewDelcoreServiceClient(&http.Client{Timeout: 15 * time.Second}, *endpoint)
	r := connect.NewRequest(&pb.GetCurrentUserRequest{})
	r.Header().Set("Authorization", "Bearer "+token)
	response, e := client.GetCurrentUser(ctx, r)
	if e != nil {
		fmt.Fprintln(os.Stderr, "tenant discovery failed")
		os.Exit(1)
	}
	fmt.Printf("Discovered %d tenants; correlation %s\n", len(response.Msg.Tenants), response.Msg.Metadata.CorrelationId)
}
