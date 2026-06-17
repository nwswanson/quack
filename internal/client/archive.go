package client

import (
	"context"
	"io"

	"quack/internal/protocol"
)

func WriteTar(ctx context.Context, root string, w io.Writer) error {
	return protocol.WriteTar(ctx, root, w)
}
