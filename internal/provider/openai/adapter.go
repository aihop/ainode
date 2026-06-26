package openai

import (
	"bytes"
	"io"
	"net/http"
	"strconv"

	"aihop.io/ainode/internal/provider"
)

// Adapter 官方协议，透传不做转换。
type Adapter struct{}

var (
	SharedRequestAdapter = &Adapter{}
	SharedAuthStrategy   = provider.HeaderAuthStrategy{Header: "Authorization", Prefix: "Bearer "}
	SharedErrorStrategy  = provider.GenericErrorTranslator{Provider: "openai"}
)

func (a *Adapter) RewriteRequest(req *http.Request, modelName string) error {
	if req.Body == nil || modelName == "" {
		return nil
	}

	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return err
	}

	rewrittenBody, err := provider.RewriteRequestBodyModel(bodyBytes, modelName)
	if err != nil {
		return err
	}

	req.Body = io.NopCloser(bytes.NewBuffer(rewrittenBody))
	req.ContentLength = int64(len(rewrittenBody))
	req.Header.Set("Content-Length", strconv.Itoa(len(rewrittenBody)))
	return nil
}

func (a *Adapter) TransformSSEEvent(event []byte) ([]byte, error) {
	return event, nil
}
