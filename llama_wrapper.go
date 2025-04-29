package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// LlamaClient wraps the LLaMA.cpp HTTP server endpoints.
type LlamaClient struct {
	BaseURL string
	APIKey  string // optional
}

// NewLlamaClient creates a new client.
func NewLlamaClient(baseURL string) *LlamaClient {
	return &LlamaClient{BaseURL: strings.TrimRight(baseURL, "/")}
}

// doRequest is a helper for HTTP requests.
func (c *LlamaClient) doRequest(method, path string, body interface{}, result interface{}) error {
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		buf = bytes.NewBuffer(b)
	}
	req, err := http.NewRequest(method, c.BaseURL+path, buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if result != nil {
		return json.NewDecoder(resp.Body).Decode(result)
	}
	return nil
}

// Health checks server health.
func (c *LlamaClient) Health() (map[string]interface{}, error) {
	var res map[string]interface{}
	err := c.doRequest("GET", "/health", nil, &res)
	return res, err
}

// Completion sends a prompt and options to /completion.
func (c *LlamaClient) Completion(req map[string]interface{}) (map[string]interface{}, error) {
	var res map[string]interface{}
	err := c.doRequest("POST", "/completion", req, &res)
	return res, err
}

// Tokenize text to tokens.
func (c *LlamaClient) Tokenize(content string) ([]int, error) {
	body := map[string]interface{}{"content": content}
	var res []int
	err := c.doRequest("POST", "/tokenize", body, &res)
	return res, err
}

// Detokenize tokens to text.
func (c *LlamaClient) Detokenize(tokens []int) (string, error) {
	body := map[string]interface{}{"tokens": tokens}
	var res string
	err := c.doRequest("POST", "/detokenize", body, &res)
	return res, err
}

// Embedding for text (and optional image_data).
func (c *LlamaClient) Embedding(req map[string]interface{}) (map[string]interface{}, error) {
	var res map[string]interface{}
	err := c.doRequest("POST", "/embedding", req, &res)
	return res, err
}

// Infill for code infilling.
func (c *LlamaClient) Infill(req map[string]interface{}) (map[string]interface{}, error) {
	var res map[string]interface{}
	err := c.doRequest("POST", "/infill", req, &res)
	return res, err
}

// Props returns server properties.
func (c *LlamaClient) Props() (map[string]interface{}, error) {
	var res map[string]interface{}
	err := c.doRequest("GET", "/props", nil, &res)
	return res, err
}

// Slots returns current slots state.
func (c *LlamaClient) Slots() ([]map[string]interface{}, error) {
	var res []map[string]interface{}
	err := c.doRequest("GET", "/slots", nil, &res)
	return res, err
}

// Metrics returns Prometheus metrics as plain text.
func (c *LlamaClient) Metrics() (string, error) {
	req, err := http.NewRequest("GET", c.BaseURL+"/metrics", nil)
	if err != nil {
		return "", err
	}
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

// OpenAI-compatible chat completions.
func (c *LlamaClient) ChatCompletions(req map[string]interface{}) (map[string]interface{}, error) {
	var res map[string]interface{}
	err := c.doRequest("POST", "/v1/chat/completions", req, &res)
	return res, err
}

// OpenAI-compatible embeddings.
func (c *LlamaClient) OpenAIEmbeddings(req map[string]interface{}) (map[string]interface{}, error) {
	var res map[string]interface{}
	err := c.doRequest("POST", "/v1/embeddings", req, &res)
	return res, err
}
