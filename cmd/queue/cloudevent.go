package main

import (
	"context"
	"fmt"
	"log"

	"github.com/cloudevents/sdk-go/pkg/cloudevents"
	"github.com/cloudevents/sdk-go/pkg/cloudevents/client"
	"github.com/cloudevents/sdk-go/pkg/cloudevents/types"
	"github.com/knative/eventing-sources/pkg/kncloudevents"
)

type Request struct {
	Url    string
	Params map[string][]string
	Body   []byte
}

func NewClient(sink string) client.Client {
	c, err := kncloudevents.NewDefaultClient(sink)
	if err != nil {
		log.Fatalf("failed to create client: %s", err.Error())
	}
	return c
}

func SendInfo(c client.Client, r Request, app, guid string) error {
	source := types.ParseURLRef(fmt.Sprintf("https://app.url/#%s/%s", app, guid))

	event := cloudevents.Event{
		Context: cloudevents.EventContextV02{
			Type:   "dev.knative.eventing.async",
			Source: *source,
			Extensions: map[string]interface{}{
				"app": app,
			},
		}.AsV02(),
		Data: r,
	}

	if _, err := c.Send(context.Background(), event); err != nil {
		return err
	}

	return nil
}
