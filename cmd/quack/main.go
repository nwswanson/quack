package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"quack/internal/client"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var resp any
	var err error
	switch os.Args[1] {
	case "deploy":
		resp, err = runDeploy(os.Args[2:])
	case "delete":
		resp, err = runDelete(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(resp)
}

func runDeploy(args []string) (any, error) {
	values, positionals, err := parseCommandArgs(args)
	if err != nil {
		return nil, err
	}
	if len(positionals) != 2 {
		return nil, fmt.Errorf("usage: quack deploy <directory> <site name> --token <token> --serverURL <url>")
	}

	return client.UploadDirectory(context.Background(), values.serverURL, values.token, positionals[1], positionals[0])
}

func runDelete(args []string) (any, error) {
	values, positionals, err := parseCommandArgs(args)
	if err != nil {
		return nil, err
	}
	if len(positionals) != 1 {
		return nil, fmt.Errorf("usage: quack delete <site name> --token <token> --serverURL <url>")
	}

	return client.DeleteSite(context.Background(), values.serverURL, values.token, positionals[0])
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  quack deploy <directory> <site name> --token <token> --serverURL <url>")
	fmt.Fprintln(os.Stderr, "  quack delete <site name> --token <token> --serverURL <url>")
}

type commandValues struct {
	token     string
	serverURL string
}

func parseCommandArgs(args []string) (commandValues, []string, error) {
	var values commandValues
	var positionals []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, hasValue := strings.Cut(arg, "=")
		switch name {
		case "--token":
			if !hasValue {
				i++
				if i >= len(args) {
					return values, nil, fmt.Errorf("--token requires a value")
				}
				value = args[i]
			}
			values.token = value
		case "--serverURL":
			if !hasValue {
				i++
				if i >= len(args) {
					return values, nil, fmt.Errorf("--serverURL requires a value")
				}
				value = args[i]
			}
			values.serverURL = value
		default:
			if strings.HasPrefix(arg, "-") {
				return values, nil, fmt.Errorf("unknown flag: %s", arg)
			}
			positionals = append(positionals, arg)
		}
	}
	return values, positionals, nil
}
