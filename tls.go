package main

import (
    "crypto/tls"
    "fmt"
)

type TLSConfig struct {
    CertFile string
    KeyFile  string
}

func LoadTLSConfig(certFile, keyFile string) (*tls.Config, error) {
    if certFile == "" || keyFile == "" {
        return nil, nil
    }

    cert, err := tls.LoadX509KeyPair(certFile, keyFile)
    if err != nil {
        return nil, fmt.Errorf("load tls cert: %w", err)
    }

    return &tls.Config{
        Certificates: []tls.Certificate{cert},
        MinVersion:   tls.VersionTLS12,
    }, nil
}
