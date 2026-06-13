package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"quack/internal/client"
)

func main() {
	serverURL := flag.String("server", "", "upload server URL")
	token := flag.String("token", "", "upload bearer token")
	site := flag.String("site", "", "site key for this upload")
	directory := flag.String("directory", "", "directory to upload")
	flag.Parse()

	resp, err := client.UploadDirectory(context.Background(), *serverURL, *token, *site, *directory)
	if resp != nil {
		_ = json.NewEncoder(os.Stdout).Encode(resp)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
