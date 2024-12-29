package chopsticks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"sync"

	"github.com/gorilla/mux"
	"github.com/vulpemventures/nigiri/internal/docker"
	"github.com/vulpemventures/nigiri/internal/rpc"
)

type Server struct {
	bitcoinServer  *http.Server
	liquidServer   *http.Server
	bitcoinRouter  *mux.Router
	liquidRouter   *mux.Router
	stopChan       chan struct{}
	bitcoinErrChan chan error
	liquidErrChan  chan error
	bitcoinClient  *rpc.Client
	liquidClient   *rpc.Client
	electrsPort    string
	liquidElectrsPort string
	registry       map[string]AssetInfo
	registryMu     sync.RWMutex
	composePath    string
	logFile        *os.File
}

type AssetInfo struct {
	Name   string `json:"name"`
	Ticker string `json:"ticker"`
}

type FaucetRequest struct {
	Address string  `json:"address"`
	Amount  float64 `json:"amount"`
	Asset   string  `json:"asset,omitempty"` // Only for Liquid
}

type MintRequest struct {
	Address  string  `json:"address"`
	Quantity float64 `json:"quantity"`
	Name     string  `json:"name"`
	Ticker   string  `json:"ticker"`
}

func New(bitcoinPort, liquidPort, composePath, logPath string) *Server {
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Printf("Error opening log file: %v\n", err)
		return nil
	}

	s := &Server{
		bitcoinRouter:     mux.NewRouter(),
		liquidRouter:      mux.NewRouter(),
		stopChan:         make(chan struct{}),
		bitcoinErrChan:   make(chan error, 1),
		liquidErrChan:    make(chan error, 1),
		electrsPort:      bitcoinPort,
		liquidElectrsPort: liquidPort,
		registry:         make(map[string]AssetInfo),
		composePath:      composePath,
		logFile:          logFile,
	}

	return s
}

func (s *Server) log(format string, args ...interface{}) {
	if s.logFile != nil {
		fmt.Fprintf(s.logFile, format+"\n", args...)
	}
}

func (s *Server) Start() error {
	s.log("Starting chopsticks servers...")

	err := docker.WaitForService(s.composePath, "bitcoin")
	if err != nil {
		s.log("Bitcoin service not ready: %v", err)
		return fmt.Errorf("bitcoin service not ready: %w", err)
	}

	err = docker.WaitForService(s.composePath, "liquid")
	if err != nil {
		s.log("Liquid service not ready: %v", err)
		return fmt.Errorf("liquid service not ready: %w", err)
	}

	err = docker.WaitForService(s.composePath, "electrs")
	if err != nil {
		s.log("Electrs service not ready: %v", err)
		return fmt.Errorf("electrs service not ready: %w", err)
	}

	bitcoinRPCPorts, err := docker.GetPortsForService(s.composePath, "bitcoin")
	if err != nil {
		s.log("Failed to get Bitcoin RPC port: %v", err)
		return fmt.Errorf("failed to get Bitcoin RPC port: %w", err)
	}
	if len(bitcoinRPCPorts) == 0 {
		s.log("No ports found for Bitcoin service")
		return fmt.Errorf("no ports found for Bitcoin service")
	}
	bitcoinEndpoint := fmt.Sprintf("http://127.0.0.1:%s", bitcoinRPCPorts[0])
	s.log("Bitcoin RPC endpoint: %s", bitcoinEndpoint)

	liquidRPCPorts, err := docker.GetPortsForService(s.composePath, "liquid")
	if err != nil {
		s.log("Failed to get Liquid RPC port: %v", err)
		return fmt.Errorf("failed to get Liquid RPC port: %w", err)
	}
	if len(liquidRPCPorts) == 0 {
		s.log("No ports found for Liquid service")
		return fmt.Errorf("no ports found for Liquid service")
	}
	liquidEndpoint := fmt.Sprintf("http://127.0.0.1:%s", liquidRPCPorts[0])
	s.log("Liquid RPC endpoint: %s", liquidEndpoint)

	s.bitcoinClient = rpc.NewClient(bitcoinEndpoint, "admin1", "123")
	s.liquidClient = rpc.NewClient(liquidEndpoint, "admin1", "123")

	err = s.createWalletIfNotExists()
	if err != nil {
		s.log("Failed to create wallet: %v", err)
		return fmt.Errorf("failed to create wallet: %w", err)
	}

	s.bitcoinServer = &http.Server{
		Addr:    fmt.Sprintf("0.0.0.0:%s", s.electrsPort),
		Handler: s.bitcoinHandler(),
	}

	s.liquidServer = &http.Server{
		Addr:    fmt.Sprintf("0.0.0.0:%s", s.liquidElectrsPort),
		Handler: s.liquidHandler(),
	}

	go func() {
		s.log("Starting Bitcoin server on port %s", s.electrsPort)
		if err := s.bitcoinServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.bitcoinErrChan <- fmt.Errorf("bitcoin server error: %w", err)
		}
	}()

	go func() {
		s.log("Starting Liquid server on port %s", s.liquidElectrsPort)
		if err := s.liquidServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.liquidErrChan <- fmt.Errorf("liquid server error: %w", err)
		}
	}()

	select {
	case <-s.stopChan:
		s.log("Received stop signal")
		return s.Stop()
	case err := <-s.bitcoinErrChan:
		s.log("Bitcoin server error: %v", err)
		return err
	case err := <-s.liquidErrChan:
		s.log("Liquid server error: %v", err)
		return err
	}
}

func (s *Server) setupBitcoinHandlers(proxy *httputil.ReverseProxy) {
	s.AddFaucetHandlerBTC(s.bitcoinRouter)
	s.AddTxHandlerBTC(s.bitcoinRouter)

	s.bitcoinRouter.PathPrefix("/").Handler(&corsHandler{proxy})
}

type corsHandler struct {
	h http.Handler
}

func (ch *corsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, Authorization")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	ch.h.ServeHTTP(w, r)
}

func (s *Server) setupLiquidHandlers(proxy *httputil.ReverseProxy) {
	s.AddFaucetHandlerLiquid(s.liquidRouter)
	s.AddTxHandlerLiquid(s.liquidRouter)
	s.AddMintHandler(s.liquidRouter)
	s.AddRegistryHandler(s.liquidRouter)

	s.liquidRouter.PathPrefix("/").Handler(&corsHandler{proxy})
}

func (s *Server) Stop() error {
	s.log("Stopping chopsticks servers...")

	if s.bitcoinServer != nil {
		if err := s.bitcoinServer.Shutdown(context.Background()); err != nil {
			s.log("Error stopping Bitcoin server: %v", err)
		}
	}

	if s.liquidServer != nil {
		if err := s.liquidServer.Shutdown(context.Background()); err != nil {
			s.log("Error stopping Liquid server: %v", err)
		}
	}

	if s.logFile != nil {
		if err := s.logFile.Close(); err != nil {
			fmt.Printf("Error closing log file: %v\n", err)
		}
	}

	return nil
}

func (s *Server) AddFaucetHandlerBTC(router *mux.Router) {
	router.HandleFunc("/faucet", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req FaucetRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request format", http.StatusBadRequest)
			return
		}

		result, err := s.bitcoinClient.Call("sendtoaddress", []interface{}{req.Address, req.Amount})
		if err != nil {
			http.Error(w, fmt.Sprintf("RPC error: %v", err), http.StatusInternalServerError)
			return
		}

		if _, err := s.bitcoinClient.Call("generatetoaddress", []interface{}{1, req.Address}); err != nil {
			s.log("Warning: failed to mine block after faucet: %v", err)
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"txid": result,
		})
	})
}

func (s *Server) AddFaucetHandlerLiquid(router *mux.Router) {
	router.HandleFunc("/faucet", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req FaucetRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request format", http.StatusBadRequest)
			return
		}

		params := []interface{}{req.Address, req.Amount, "", "", false, false, 1, "UNSET", false}
		if req.Asset != "" {
			params = append(params, req.Asset)
		}

		result, err := s.liquidClient.Call("sendtoaddress", params)
		if err != nil {
			http.Error(w, fmt.Sprintf("RPC error: %v", err), http.StatusInternalServerError)
			return
		}

		if _, err := s.liquidClient.Call("generatetoaddress", []interface{}{1, req.Address}); err != nil {
			s.log("Warning: failed to mine block after faucet: %v", err)
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"txid": result,
		})
	})
}

func (s *Server) AddTxHandlerBTC(router *mux.Router) {
	router.HandleFunc("/tx", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		txHex, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Error reading request body", http.StatusBadRequest)
			return
		}

		result, err := s.bitcoinClient.Call("sendrawtransaction", []interface{}{string(txHex)})
		if err != nil {
			http.Error(w, fmt.Sprintf("RPC error: %v", err), http.StatusInternalServerError)
			return
		}

		if _, err := s.bitcoinClient.Call("generatetoaddress", []interface{}{1, "bcrt1qw508d6qejxtdg4y5r3zarvary0c5xw7kygt080"}); err != nil {
			s.log("Warning: failed to mine block after broadcast: %v", err)
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"txid": result,
		})
	})
}

func (s *Server) AddTxHandlerLiquid(router *mux.Router) {
	router.HandleFunc("/tx", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		txHex, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Error reading request body", http.StatusBadRequest)
			return
		}

		result, err := s.liquidClient.Call("sendrawtransaction", []interface{}{string(txHex)})
		if err != nil {
			http.Error(w, fmt.Sprintf("RPC error: %v", err), http.StatusInternalServerError)
			return
		}

		if _, err := s.liquidClient.Call("generatetoaddress", []interface{}{1, "el1qqw508d6qejxtdg4y5r3zarvary0c5xw7kq3h39k89lzgtlkr56h7ckq92pmn7"}); err != nil {
			s.log("Warning: failed to mine block after broadcast: %v", err)
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"txid": result,
		})
	})
}

func (s *Server) AddMintHandler(router *mux.Router) {
	router.HandleFunc("/mint", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req MintRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request format", http.StatusBadRequest)
			return
		}

		result, err := s.liquidClient.Call("issueasset", []interface{}{req.Quantity, 0})
		if err != nil {
			http.Error(w, fmt.Sprintf("RPC error: %v", err), http.StatusInternalServerError)
			return
		}

		assetData := result.(map[string]interface{})
		assetID := assetData["asset"].(string)

		s.registryMu.Lock()
		s.registry[assetID] = AssetInfo{
			Name:   req.Name,
			Ticker: req.Ticker,
		}
		s.registryMu.Unlock()

		_, err = s.liquidClient.Call("sendtoaddress", []interface{}{req.Address, req.Quantity, "", "", false, false, 1, "UNSET", false, assetID})
		if err != nil {
			http.Error(w, fmt.Sprintf("RPC error: %v", err), http.StatusInternalServerError)
			return
		}

		if _, err := s.liquidClient.Call("generatetoaddress", []interface{}{1, req.Address}); err != nil {
			s.log("Warning: failed to mine block after mint: %v", err)
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"asset": assetID,
		})
	})
}

func (s *Server) AddRegistryHandler(router *mux.Router) {
	router.HandleFunc("/registry", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		s.registryMu.RLock()
		defer s.registryMu.RUnlock()

		json.NewEncoder(w).Encode(s.registry)
	})
}

func (s *Server) bitcoinHandler() http.Handler {
	bitcoinTarget, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%s", s.electrsPort))
	bitcoinProxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = bitcoinTarget.Scheme
			req.URL.Host = bitcoinTarget.Host
			s.log("Proxying request to: %s%s", req.URL.Host, req.URL.Path)
		},
	}

	s.setupBitcoinHandlers(bitcoinProxy)

	return s.bitcoinRouter
}

func (s *Server) liquidHandler() http.Handler {
	liquidTarget, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%s", s.liquidElectrsPort))
	liquidProxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = liquidTarget.Scheme
			req.URL.Host = liquidTarget.Host
			s.log("Proxying request to: %s%s", req.URL.Host, req.URL.Path)
		},
	}

	s.setupLiquidHandlers(liquidProxy)

	return s.liquidRouter
}

func (s *Server) createWalletIfNotExists() error {
	err := s.bitcoinClient.CreateWallet("default")
	if err != nil {
		return fmt.Errorf("error creating Bitcoin wallet: %w", err)
	}

	address, err := s.bitcoinClient.GetNewAddress()
	if err != nil {
		return fmt.Errorf("error getting Bitcoin address: %w", err)
	}

	err = s.bitcoinClient.GenerateToAddress(address, 101)
	if err != nil {
		return fmt.Errorf("error generating Bitcoin blocks: %w", err)
	}

	err = s.liquidClient.CreateWallet("default")
	if err != nil {
		return fmt.Errorf("error creating Liquid wallet: %w", err)
	}

	address, err = s.liquidClient.GetNewAddress()
	if err != nil {
		return fmt.Errorf("error getting Liquid address: %w", err)
	}

	err = s.liquidClient.GenerateToAddress(address, 101)
	if err != nil {
		return fmt.Errorf("error generating Liquid blocks: %w", err)
	}

	return nil
}
