package rpc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	endpoint string
	username string
	password string
}

type RPCRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
	ID      int          `json:"id"`
}

type RPCResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *RPCError      `json:"error"`
	ID     int            `json:"id"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func NewClient(endpoint string, username string, password string) *Client {
	return &Client{
		endpoint: endpoint + "/wallet/",
		username: username,
		password: password,
	}
}

func (c *Client) Call(method string, params []interface{}) (interface{}, error) {
	var lastErr error
	maxRetries := 5
	retryDelay := time.Second

	for i := 0; i < maxRetries; i++ {
		result, err := c.doCall(method, params)
		if err == nil {
			return result, nil
		}
		lastErr = err
		fmt.Printf("RPC call attempt %d failed: %v\n", i+1, err)
		time.Sleep(retryDelay)
		retryDelay *= 2 // exponential backoff
	}

	return nil, fmt.Errorf("all retries failed: %w", lastErr)
}

func (c *Client) doCall(method string, params []interface{}) (interface{}, error) {
	request := RPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      1,
	}

	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("error marshaling request: %w", err)
	}

	// Debug request
	fmt.Printf("RPC Request: %s\n", string(body))

	req, err := http.NewRequest("POST", c.endpoint, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.username, c.password)

	// Add regtest parameter
	q := req.URL.Query()
	q.Add("chain", "regtest")
	req.URL.RawQuery = q.Encode()

	// Debug headers
	fmt.Printf("Request Headers: %v\n", req.Header)
	fmt.Printf("Request URL: %s\n", req.URL.String())

	// Create a new client with longer timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error making request: %w", err)
	}
	defer resp.Body.Close()

	// Debug response status
	fmt.Printf("HTTP Status: %d\n", resp.StatusCode)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("Error response body: %s\n", string(body))
		return nil, fmt.Errorf("HTTP error: %d - %s", resp.StatusCode, string(body))
	}

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response: %w", err)
	}

	// Debug response
	fmt.Printf("RPC Response: %s\n", string(body))

	var response RPCResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("error unmarshaling response: %w", err)
	}

	if response.Error != nil {
		return nil, fmt.Errorf("RPC error: %v", response.Error)
	}

	var result interface{}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return nil, fmt.Errorf("error unmarshaling result: %w", err)
	}

	return result, nil
}

func (c *Client) CreateWallet(name string) error {
	// Check if wallet already exists
	wallets, err := c.ListWallets()
	if err != nil {
		return fmt.Errorf("error listing wallets: %w", err)
	}

	for _, wallet := range wallets {
		if wallet == name {
			fmt.Printf("Wallet %s already exists\n", name)
			return nil
		}
	}

	// Create wallet
	_, err = c.Call("createwallet", []interface{}{name})
	if err != nil {
		return fmt.Errorf("error creating wallet: %w", err)
	}

	fmt.Printf("Created wallet: %s\n", name)
	return nil
}

func (c *Client) ListWallets() ([]string, error) {
	result, err := c.Call("listwallets", []interface{}{})
	if err != nil {
		return nil, fmt.Errorf("error listing wallets: %w", err)
	}

	wallets, ok := result.([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected result type: %T", result)
	}

	var walletNames []string
	for _, wallet := range wallets {
		name, ok := wallet.(string)
		if !ok {
			return nil, fmt.Errorf("unexpected wallet name type: %T", wallet)
		}
		walletNames = append(walletNames, name)
	}

	return walletNames, nil
}

func (c *Client) GenerateToAddress(address string, blocks int) error {
	_, err := c.Call("generatetoaddress", []interface{}{blocks, address})
	if err != nil {
		return fmt.Errorf("error generating blocks: %w", err)
	}
	return nil
}

func (c *Client) GetNewAddress() (string, error) {
	result, err := c.Call("getnewaddress", []interface{}{})
	if err != nil {
		return "", fmt.Errorf("error getting new address: %w", err)
	}

	address, ok := result.(string)
	if !ok {
		return "", fmt.Errorf("unexpected result type: %T", result)
	}

	return address, nil
}
