package llama_wrapper

import (
	"bytes"
	"encoding/json"
	log "github.com/sirupsen/logrus"
	"io"
	"net/http"
	"strings"
	"time"
)

// LlamaClient wraps the LLaMA.cpp HTTP server endpoints.
type LlamaClient struct {
	BaseURL string
	APIKey  string // optional
}
type tokenizeResponse struct {
	Tokens []int `json:"tokens"`
}

// NewLlamaClient creates a new client.
func NewLlamaClient(baseURL string) *LlamaClient {
	return &LlamaClient{BaseURL: strings.TrimRight(baseURL, "/")}
}

// doRequest is a helper for HTTP requests.
func (c *LlamaClient) doRequest(method, path string, body interface{}, result interface{}) error {
	startTime := time.Now()
	defer func() {
		log.Debugf("LlamaClient.doRequest %s %s took %s", method, path, time.Since(startTime))
	}()

	var buf io.Reader
	if body != nil {
		marshalStartTime := time.Now()
		b, err := json.Marshal(body)
		if err != nil {
			log.Errorf("LlamaClient.doRequest failed to marshal body for %s %s: %v", method, path, err)
			return err
		}
		log.Debugf("LlamaClient.doRequest JSON marshal for %s %s took %s", method, path, time.Since(marshalStartTime))
		buf = bytes.NewBuffer(b)
	}
	req, err := http.NewRequest(method, c.BaseURL+path, buf)
	if err != nil {
		log.Errorf("LlamaClient.doRequest failed to create new request for %s %s: %v", method, path, err)
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	httpStartTime := time.Now()
	resp, err := http.DefaultClient.Do(req)
	log.Debugf("LlamaClient.doRequest HTTP call for %s %s took %s", method, path, time.Since(httpStartTime))
	if err != nil {
		log.Errorf("LlamaClient.doRequest HTTP Do failed for %s %s: %v", method, path, err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		log.Errorf("LlamaClient.doRequest %s %s returned error status %d: %s", method, path, resp.StatusCode, string(bodyBytes))
		// Re-assign resp.Body as ReadAll consumes it
		resp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	}

	if result != nil {
		decodeStartTime := time.Now()
		err = json.NewDecoder(resp.Body).Decode(result)
		log.Debugf("LlamaClient.doRequest JSON decode for %s %s took %s", method, path, time.Since(decodeStartTime))
		if err != nil {
			log.Errorf("LlamaClient.doRequest failed to decode response for %s %s: %v", method, path, err)
			return err
		}
	}
	return nil
}

// Health checks server health.
func (c *LlamaClient) Health() (map[string]interface{}, error) {
	startTime := time.Now()
	defer func() {
		log.Debugf("LlamaClient.Health took %s", time.Since(startTime))
	}()
	var res map[string]interface{}
	err := c.doRequest("GET", "/health", nil, &res)
	return res, err
}

// Completion sends a prompt and options to /completion.
func (c *LlamaClient) Completion(req map[string]interface{}) (map[string]interface{}, error) {
	startTime := time.Now()
	defer func() {
		log.Debugf("LlamaClient.Completion took %s", time.Since(startTime))
	}()
	// log.Debugf("LlamaClient.Completion prompt: %s", req["prompt"])
	var res map[string]interface{}
	err := c.doRequest("POST", "/completion", req, &res)
	return res, err
}

// Tokenize text to tokens.
func (c *LlamaClient) Tokenize(content string) ([]int, error) {
	startTime := time.Now()
	defer func() {
		log.Debugf("LlamaClient.Tokenize for content length %d took %s", len(content), time.Since(startTime))
	}()
	body := map[string]interface{}{"content": content}
	var res tokenizeResponse
	err := c.doRequest("POST", "/tokenize", body, &res)
	return res.Tokens, err
}

// Detokenize tokens to text.
func (c *LlamaClient) Detokenize(tokens []int) (string, error) {
	startTime := time.Now()
	defer func() {
		log.Debugf("LlamaClient.Detokenize for %d tokens took %s", len(tokens), time.Since(startTime))
	}()
	body := map[string]interface{}{"tokens": tokens}
	var res string                                        // Expecting a simple string response based on typical detokenize endpoints
	err := c.doRequest("POST", "/detokenize", body, &res) // Assuming the response is directly the string
	return res, err
}

// Embedding for text (and optional image_data).
func (c *LlamaClient) Embedding(req map[string]interface{}) (map[string]interface{}, error) {
	startTime := time.Now()
	defer func() {
		log.Debugf("LlamaClient.Embedding took %s", time.Since(startTime))
	}()
	var res map[string]interface{}
	err := c.doRequest("POST", "/embedding", req, &res)
	return res, err
}

// Infill for code infilling.
func (c *LlamaClient) Infill(req map[string]interface{}) (map[string]interface{}, error) {
	startTime := time.Now()
	defer func() {
		log.Debugf("LlamaClient.Infill took %s", time.Since(startTime))
	}()
	var res map[string]interface{}
	err := c.doRequest("POST", "/infill", req, &res)
	return res, err
}

// Props returns server properties.
func (c *LlamaClient) Props() (map[string]interface{}, error) {
	startTime := time.Now()
	defer func() {
		log.Debugf("LlamaClient.Props took %s", time.Since(startTime))
	}()
	var res map[string]interface{}
	err := c.doRequest("GET", "/props", nil, &res)
	return res, err
}

// Slots returns current slots state.
func (c *LlamaClient) Slots() ([]map[string]interface{}, error) {
	startTime := time.Now()
	defer func() {
		log.Debugf("LlamaClient.Slots took %s", time.Since(startTime))
	}()
	var res []map[string]interface{}
	err := c.doRequest("GET", "/slots", nil, &res)
	return res, err
}

// Metrics returns Prometheus metrics as plain text.
// This method does not use doRequest, so logging is added directly.
func (c *LlamaClient) Metrics() (string, error) {
	startTime := time.Now()
	defer func() {
		log.Debugf("LlamaClient.Metrics took %s", time.Since(startTime))
	}()
	req, err := http.NewRequest("GET", c.BaseURL+"/metrics", nil)
	if err != nil {
		log.Errorf("LlamaClient.Metrics failed to create request: %v", err)
		return "", err
	}
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	httpStartTime := time.Now()
	resp, err := http.DefaultClient.Do(req)
	log.Debugf("LlamaClient.Metrics HTTP call took %s", time.Since(httpStartTime))
	if err != nil {
		log.Errorf("LlamaClient.Metrics HTTP Do failed: %v", err)
		return "", err
	}
	defer resp.Body.Close()

	readStartTime := time.Now()
	b, err := io.ReadAll(resp.Body)
	log.Debugf("LlamaClient.Metrics io.ReadAll took %s", time.Since(readStartTime))
	if err != nil {
		log.Errorf("LlamaClient.Metrics failed to read response body: %v", err)
		return "", err
	}
	return string(b), err
}

// OpenAI-compatible chat completions.
func (c *LlamaClient) ChatCompletions(req map[string]interface{}) (map[string]interface{}, error) {
	startTime := time.Now()
	defer func() {
		log.Debugf("LlamaClient.ChatCompletions took %s", time.Since(startTime))
	}()
	var res map[string]interface{}
	err := c.doRequest("POST", "/v1/chat/completions", req, &res)
	return res, err
}

// OpenAI-compatible embeddings.
func (c *LlamaClient) OpenAIEmbeddings(req map[string]interface{}) (map[string]interface{}, error) {
	startTime := time.Now()
	defer func() {
		log.Debugf("LlamaClient.OpenAIEmbeddings took %s", time.Since(startTime))
	}()
	var res map[string]interface{}
	err := c.doRequest("POST", "/v1/embeddings", req, &res)
	return res, err
}
