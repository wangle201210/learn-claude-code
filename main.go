package main

import (
	"context"

	"github.com/cloudwego/eino/schema"
)

func main() {
	ctx := context.Background()
	model, err := NewModel(ctx)
	if err != nil {
		panic(err)
	}
	msg, err := model.Generate(ctx, []*schema.Message{{Content: "Hello!"}})
	if err != nil {
		panic(err)
	}
	println(msg.Content)
}
