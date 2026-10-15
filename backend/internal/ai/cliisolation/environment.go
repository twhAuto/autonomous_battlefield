package cliisolation

import "os"

// Variables a CLI that keeps its native login under HOME needs to authenticate and
// reach the network. Provider keys, CLI overrides, and agent secrets never survive.
var loginEnvironmentKeys = []string{"PATH", "HOME", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy", "SSL_CERT_FILE", "SSL_CERT_DIR", "LANG"}

func inheritEnvironment(keys []string) []string {
	environment := []string{}
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			environment = append(environment, key+"="+value)
		}
	}
	return environment
}
