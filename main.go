package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/webhook"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"gopkg.in/yaml.v3"
)

// ProxyConfig holds proxy configuration for storage providers that support it
type ProxyConfig struct {
	URL      string `yaml:"url" json:"url"`
	Username string `yaml:"username" json:"username"`
	Password string `yaml:"password" json:"password"`
}

// S3Config holds S3 storage configuration
type S3Config struct {
	AccessKey       string        `yaml:"access_key" json:"access_key"`
	Secret          string        `yaml:"secret" json:"secret"`
	SessionToken    string        `yaml:"session_token" json:"session_token"`
	Region          string        `yaml:"region" json:"region"`
	Endpoint        string        `yaml:"endpoint" json:"endpoint"`
	Bucket          string        `yaml:"bucket" json:"bucket"`
	ProxyConfig     *ProxyConfig  `yaml:"proxy_config" json:"proxy_config"`
	MaxRetries      int           `yaml:"max_retries" json:"max_retries"`
	MaxRetryDelay   time.Duration `yaml:"max_retry_delay" json:"max_retry_delay"`
	MinRetryDelay   time.Duration `yaml:"min_retry_delay" json:"min_retry_delay"`
	AWSLogLevel     string        `yaml:"aws_log_level" json:"aws_log_level"`
}

// AzureConfig holds Azure Blob Storage configuration
type AzureConfig struct {
	AccountName   string `yaml:"account_name" json:"account_name"`
	AccountKey    string `yaml:"account_key" json:"account_key"`
	ContainerName string `yaml:"container_name" json:"container_name"`
}

// GCPConfig holds Google Cloud Storage configuration
type GCPConfig struct {
	CredentialsJSON string       `yaml:"credentials_json" json:"credentials_json"`
	Bucket          string       `yaml:"bucket" json:"bucket"`
	ProxyConfig     *ProxyConfig `yaml:"proxy_config" json:"proxy_config"`
}

// AliOSSConfig holds Alibaba Cloud OSS configuration
type AliOSSConfig struct {
	AccessKey string `yaml:"access_key" json:"access_key"`
	Secret    string `yaml:"secret" json:"secret"`
	Region    string `yaml:"region" json:"region"`
	Endpoint  string `yaml:"endpoint" json:"endpoint"`
	Bucket    string `yaml:"bucket" json:"bucket"`
}

// StorageConfig holds all possible storage configurations
type StorageConfig struct {
	S3     *S3Config     `yaml:"s3" json:"s3"`
	Azure  *AzureConfig  `yaml:"azure" json:"azure"`
	GCP    *GCPConfig    `yaml:"gcp" json:"gcp"`
	AliOSS *AliOSSConfig `yaml:"alioss" json:"alioss"`
}

// Config holds the application configuration
type Config struct {
	LiveKitHost      string         `yaml:"livekit_host" json:"livekit_host"`
	LiveKitAPIKey    string         `yaml:"livekit_api_key" json:"livekit_api_key"`
	LiveKitAPISecret string         `yaml:"livekit_api_secret" json:"livekit_api_secret"`
	WebhookAPIKey    string         `yaml:"webhook_api_key" json:"webhook_api_key"`
	ListenPort       string         `yaml:"listen_port" json:"listen_port"`
	Storage          StorageConfig  `yaml:"storage" json:"storage"`
	Layout           string         `yaml:"layout" json:"layout"`
	FileType         string         `yaml:"file_type" json:"file_type"`
	FilePath         string         `yaml:"file_path" json:"file_path"`
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	egressClient := lksdk.NewEgressClient(cfg.LiveKitHost, cfg.LiveKitAPIKey, cfg.LiveKitAPISecret)
	authProvider := auth.NewSimpleKeyProvider(cfg.LiveKitAPIKey, cfg.LiveKitAPISecret)

	handler := &WebhookHandler{
		EgressClient: egressClient,
		AuthProvider: authProvider,
		Config:       cfg,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/webhook-receiver", handler.ServeHTTP)

	log.Printf("Starting server on port %s", cfg.ListenPort)
	if err := http.ListenAndServe(":"+cfg.ListenPort, mux); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

// WebhookHandler processes incoming webhooks from LiveKit
type WebhookHandler struct {
	EgressClient *lksdk.EgressClient
	AuthProvider auth.KeyProvider
	Config       *Config
}

func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Authenticate the webhook request
	body, err := webhook.Receive(r, h.AuthProvider)
	if err != nil {
		log.Printf("Error validating webhook: %v", err)
		http.Error(w, "Could not validate webhook", http.StatusUnauthorized)
		return
	}

	// First, extract just the event type to determine if we should process this event
	var eventTypeCheck struct {
		Event string `json:"event"`
	}
	
	if err := json.Unmarshal(body, &eventTypeCheck); err != nil {
		log.Printf("Error extracting event type from webhook: %v", err)
		http.Error(w, "Could not parse event type", http.StatusBadRequest)
		return
	}

	// Only handle the "room_started" event - silently ignore all others
	if eventTypeCheck.Event != "room_started" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Now that we know it's a room_started event, unmarshal the full event
	event := livekit.WebhookEvent{}
	if err := json.Unmarshal(body, &event); err != nil {
		log.Printf("Error unmarshalling room_started webhook event: %v", err)
		http.Error(w, "Could not unmarshal room_started event", http.StatusBadRequest)
		return
	}

	roomName := event.Room.Name
	log.Printf("Received room_started event for room: %s", roomName)

	// Start the recording process
	h.startRecording(roomName)

	w.WriteHeader(http.StatusOK)
}

func (h *WebhookHandler) startRecording(roomName string) {
	fileOutput, err := h.createFileOutput()
	if err != nil {
		log.Printf("Failed to create file output configuration: %v", err)
		return
	}

	// Parse file type
	var fileType livekit.EncodedFileType
	switch strings.ToUpper(h.Config.FileType) {
	case "MP4":
		fileType = livekit.EncodedFileType_MP4
	case "OGG":
		fileType = livekit.EncodedFileType_OGG
	default:
		fileType = livekit.EncodedFileType_MP4
	}

	// Set the file type on the output
	fileOutput.FileType = fileType

	request := &livekit.RoomCompositeEgressRequest{
		RoomName:    roomName,
		Layout:      h.Config.Layout,
		FileOutputs: []*livekit.EncodedFileOutput{fileOutput},
	}

	log.Printf("Sending StartEgress request for room: %s", roomName)
	_, err = h.EgressClient.StartRoomCompositeEgress(context.Background(), request)
	if err != nil {
		log.Printf("Failed to start egress for room %s: %v", roomName, err)
	} else {
		log.Printf("Successfully started egress for room: %s", roomName)
	}
}

func (h *WebhookHandler) createFileOutput() (*livekit.EncodedFileOutput, error) {
	storage := &h.Config.Storage

	fileOutput := &livekit.EncodedFileOutput{
		FileType: livekit.EncodedFileType_MP4, // Default, will be overridden
		Filepath: h.Config.FilePath,
	}

	// Check which storage provider is configured and create appropriate output
	if storage.S3 != nil {
		s3Config := &livekit.S3Upload{
			AccessKey:    storage.S3.AccessKey,
			Secret:       storage.S3.Secret,
			SessionToken: storage.S3.SessionToken,
			Region:       storage.S3.Region,
			Endpoint:     storage.S3.Endpoint,
			Bucket:       storage.S3.Bucket,
		}

		fileOutput.Output = &livekit.EncodedFileOutput_S3{S3: s3Config}
		return fileOutput, nil
	}

	if storage.Azure != nil {
		azureConfig := &livekit.AzureBlobUpload{
			AccountName:   storage.Azure.AccountName,
			AccountKey:    storage.Azure.AccountKey,
			ContainerName: storage.Azure.ContainerName,
		}
		fileOutput.Output = &livekit.EncodedFileOutput_Azure{Azure: azureConfig}
		return fileOutput, nil
	}

	if storage.GCP != nil {
		gcpConfig := &livekit.GCPUpload{
			Credentials: storage.GCP.CredentialsJSON,
			Bucket:      storage.GCP.Bucket,
		}

		fileOutput.Output = &livekit.EncodedFileOutput_Gcp{Gcp: gcpConfig}
		return fileOutput, nil
	}

	if storage.AliOSS != nil {
		aliOSSConfig := &livekit.AliOSSUpload{
			AccessKey: storage.AliOSS.AccessKey,
			Secret:    storage.AliOSS.Secret,
			Region:    storage.AliOSS.Region,
			Endpoint:  storage.AliOSS.Endpoint,
			Bucket:    storage.AliOSS.Bucket,
		}
		fileOutput.Output = &livekit.EncodedFileOutput_AliOSS{AliOSS: aliOSSConfig}
		return fileOutput, nil
	}

	return nil, fmt.Errorf("no storage provider configured")
}

// loadConfig loads configuration from YAML file or environment variables
func loadConfig() (*Config, error) {
	cfg := &Config{
		// Set defaults
		LiveKitHost: "http://localhost:7880",
		ListenPort:  "8080",
		Layout:      "grid",
		FileType:    "MP4",
		FilePath:    "recordings/{room_name}-{time}",
	}

	// Try to load from YAML file first
	configFile := getEnv("CONFIG_FILE", "config.yaml")
	if _, err := os.Stat(configFile); err == nil {
		log.Printf("Loading configuration from %s", configFile)
		if err := loadConfigFromYAML(configFile, cfg); err != nil {
			return nil, fmt.Errorf("failed to load YAML config: %v", err)
		}
	} else {
		log.Printf("No config file found at %s, using environment variables", configFile)
	}

	// Override with environment variables (this allows hybrid configuration)
	loadConfigFromEnv(cfg)

	// Validate required fields
	if err := validateConfig(cfg); err != nil {
		return nil, fmt.Errorf("configuration validation failed: %v", err)
	}

	return cfg, nil
}

func loadConfigFromYAML(filename string, cfg *Config) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}

	// Expand environment variables in YAML
	expanded := os.ExpandEnv(string(data))
	
	return yaml.Unmarshal([]byte(expanded), cfg)
}

func loadConfigFromEnv(cfg *Config) {
	if host := getEnv("LIVEKIT_HOST"); host != "" {
		cfg.LiveKitHost = host
	}
	if apiKey := getEnv("LIVEKIT_API_KEY"); apiKey != "" {
		cfg.LiveKitAPIKey = apiKey
	}
	if apiSecret := getEnv("LIVEKIT_API_SECRET"); apiSecret != "" {
		cfg.LiveKitAPISecret = apiSecret
	}
	if webhookKey := getEnv("WEBHOOK_API_KEY"); webhookKey != "" {
		cfg.WebhookAPIKey = webhookKey
	}
	if port := getEnv("PORT"); port != "" {
		cfg.ListenPort = port
	}
	if layout := getEnv("LAYOUT"); layout != "" {
		cfg.Layout = layout
	}
	if fileType := getEnv("FILE_TYPE"); fileType != "" {
		cfg.FileType = fileType
	}
	if filePath := getEnv("FILE_PATH"); filePath != "" {
		cfg.FilePath = filePath
	}

	// Load S3 configuration from environment variables (backward compatibility)
	if bucket := getEnv("S3_BUCKET"); bucket != "" {
		if cfg.Storage.S3 == nil {
			cfg.Storage.S3 = &S3Config{}
		}
		cfg.Storage.S3.Bucket = bucket
	}
	if accessKey := getEnv("S3_ACCESS_KEY"); accessKey != "" {
		if cfg.Storage.S3 == nil {
			cfg.Storage.S3 = &S3Config{}
		}
		cfg.Storage.S3.AccessKey = accessKey
	}
	if secret := getEnv("S3_SECRET"); secret != "" {
		if cfg.Storage.S3 == nil {
			cfg.Storage.S3 = &S3Config{}
		}
		cfg.Storage.S3.Secret = secret
	}
	if region := getEnv("S3_REGION"); region != "" {
		if cfg.Storage.S3 == nil {
			cfg.Storage.S3 = &S3Config{}
		}
		cfg.Storage.S3.Region = region
	}
	if endpoint := getEnv("S3_ENDPOINT"); endpoint != "" {
		if cfg.Storage.S3 == nil {
			cfg.Storage.S3 = &S3Config{}
		}
		cfg.Storage.S3.Endpoint = endpoint
	}
	if sessionToken := getEnv("S3_SESSION_TOKEN"); sessionToken != "" {
		if cfg.Storage.S3 == nil {
			cfg.Storage.S3 = &S3Config{}
		}
		cfg.Storage.S3.SessionToken = sessionToken
	}

	// Load Azure configuration from environment variables
	if accountName := getEnv("AZURE_STORAGE_ACCOUNT"); accountName != "" {
		if cfg.Storage.Azure == nil {
			cfg.Storage.Azure = &AzureConfig{}
		}
		cfg.Storage.Azure.AccountName = accountName
	}
	if accountKey := getEnv("AZURE_STORAGE_KEY"); accountKey != "" {
		if cfg.Storage.Azure == nil {
			cfg.Storage.Azure = &AzureConfig{}
		}
		cfg.Storage.Azure.AccountKey = accountKey
	}
	if containerName := getEnv("AZURE_CONTAINER_NAME"); containerName != "" {
		if cfg.Storage.Azure == nil {
			cfg.Storage.Azure = &AzureConfig{}
		}
		cfg.Storage.Azure.ContainerName = containerName
	}

	// Load GCP configuration from environment variables
	if credentials := getEnv("GOOGLE_APPLICATION_CREDENTIALS"); credentials != "" {
		if cfg.Storage.GCP == nil {
			cfg.Storage.GCP = &GCPConfig{}
		}
		// If it's a file path, read the file content
		if _, err := os.Stat(credentials); err == nil {
			if data, err := os.ReadFile(credentials); err == nil {
				cfg.Storage.GCP.CredentialsJSON = string(data)
			}
		} else {
			// Assume it's the JSON content directly
			cfg.Storage.GCP.CredentialsJSON = credentials
		}
	}
	if bucket := getEnv("GCP_BUCKET"); bucket != "" {
		if cfg.Storage.GCP == nil {
			cfg.Storage.GCP = &GCPConfig{}
		}
		cfg.Storage.GCP.Bucket = bucket
	}

	// Load AliOSS configuration from environment variables
	if accessKey := getEnv("ALIOSS_ACCESS_KEY"); accessKey != "" {
		if cfg.Storage.AliOSS == nil {
			cfg.Storage.AliOSS = &AliOSSConfig{}
		}
		cfg.Storage.AliOSS.AccessKey = accessKey
	}
	if secret := getEnv("ALIOSS_SECRET"); secret != "" {
		if cfg.Storage.AliOSS == nil {
			cfg.Storage.AliOSS = &AliOSSConfig{}
		}
		cfg.Storage.AliOSS.Secret = secret
	}
	if region := getEnv("ALIOSS_REGION"); region != "" {
		if cfg.Storage.AliOSS == nil {
			cfg.Storage.AliOSS = &AliOSSConfig{}
		}
		cfg.Storage.AliOSS.Region = region
	}
	if endpoint := getEnv("ALIOSS_ENDPOINT"); endpoint != "" {
		if cfg.Storage.AliOSS == nil {
			cfg.Storage.AliOSS = &AliOSSConfig{}
		}
		cfg.Storage.AliOSS.Endpoint = endpoint
	}
	if bucket := getEnv("ALIOSS_BUCKET"); bucket != "" {
		if cfg.Storage.AliOSS == nil {
			cfg.Storage.AliOSS = &AliOSSConfig{}
		}
		cfg.Storage.AliOSS.Bucket = bucket
	}
}

func validateConfig(cfg *Config) error {
	if cfg.LiveKitAPIKey == "" {
		return fmt.Errorf("LIVEKIT_API_KEY is required")
	}
	if cfg.LiveKitAPISecret == "" {
		return fmt.Errorf("LIVEKIT_API_SECRET is required")
	}
	if cfg.WebhookAPIKey == "" {
		return fmt.Errorf("WEBHOOK_API_KEY is required")
	}

	// Validate that at least one storage provider is configured
	storageCount := 0
	if cfg.Storage.S3 != nil {
		storageCount++
		if cfg.Storage.S3.Bucket == "" {
			return fmt.Errorf("S3 bucket is required when S3 storage is configured")
		}
	}
	if cfg.Storage.Azure != nil {
		storageCount++
		if cfg.Storage.Azure.AccountName == "" || cfg.Storage.Azure.AccountKey == "" || cfg.Storage.Azure.ContainerName == "" {
			return fmt.Errorf("Azure account_name, account_key, and container_name are all required when Azure storage is configured")
		}
	}
	if cfg.Storage.GCP != nil {
		storageCount++
		if cfg.Storage.GCP.CredentialsJSON == "" || cfg.Storage.GCP.Bucket == "" {
			return fmt.Errorf("GCP credentials_json and bucket are required when GCP storage is configured")
		}
	}
	if cfg.Storage.AliOSS != nil {
		storageCount++
		if cfg.Storage.AliOSS.AccessKey == "" || cfg.Storage.AliOSS.Secret == "" || cfg.Storage.AliOSS.Region == "" || cfg.Storage.AliOSS.Bucket == "" {
			return fmt.Errorf("AliOSS access_key, secret, region, and bucket are all required when AliOSS storage is configured")
		}
	}

	if storageCount == 0 {
		return fmt.Errorf("at least one storage provider must be configured")
	}
	if storageCount > 1 {
		return fmt.Errorf("only one storage provider can be configured at a time")
	}

	return nil
}

func getEnv(key string, fallback ...string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	if len(fallback) > 0 {
		return fallback[0]
	}
	return ""
}