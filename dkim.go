package main

import (
    "crypto/rand"
    "crypto/rsa"
    "crypto/sha256"
    "encoding/base64"
    "crypto/x509"
    "encoding/pem"
    "fmt"
    "strings"
)

type DKIM struct {
    Domain   string
    Selector string
    Private  *rsa.PrivateKey
}

func (d *DKIM) Sign(raw string) (string, error) {
    headers, body := splitHeaderBody(raw)

    canonBody := canonicalizeBodyRelaxed(body)
    bodyHash := sha256.Sum256([]byte(canonBody))
    bh := base64.StdEncoding.EncodeToString(bodyHash[:])

    signedHeaders := []string{"from", "to", "subject", "date", "message-id"}

    canonHeaders := canonicalizeHeadersRelaxed(headers, signedHeaders)

    dkimHeader := fmt.Sprintf(
        "v=1; a=rsa-sha256; c=relaxed/relaxed; d=%s; s=%s; h=%s; bh=%s; b=",
        d.Domain,
        d.Selector,
        strings.Join(signedHeaders, ":"),
        bh,
    )

    signingInput := canonHeaders + "dkim-signature:" + canonicalizeHeaderValue(dkimHeader)

    hash := sha256.Sum256([]byte(signingInput))

    sig, err := rsa.SignPKCS1v15(rand.Reader, d.Private, 0, hash[:])
    if err != nil {
        return "", err
    }

    b := base64.StdEncoding.EncodeToString(sig)

    fullHeader := "DKIM-Signature: " + dkimHeader + b

    return fullHeader + "\r\n" + headers + "\r\n" + body, nil
}

func splitHeaderBody(raw string) (string, string) {
    parts := strings.SplitN(raw, "\r\n\r\n", 2)
    if len(parts) != 2 {
        return raw, ""
    }
    return parts[0], parts[1]
}


func canonicalizeBodyRelaxed(body string) string {
    lines := strings.Split(body, "\n")
    for i := range lines {
        lines[i] = strings.TrimRight(lines[i], " \t\r")
    }

    // remove trailing empty lines
    i := len(lines) - 1
    for i >= 0 && strings.TrimSpace(lines[i]) == "" {
        i--
    }
    lines = lines[:i+1]

    return strings.Join(lines, "\r\n") + "\r\n"
}

func canonicalizeHeadersRelaxed(headers string, keys []string) string {
    lines := strings.Split(headers, "\r\n")
    m := map[string]string{}

    for _, l := range lines {
        if i := strings.Index(l, ":"); i > 0 {
            k := strings.ToLower(strings.TrimSpace(l[:i]))
            v := canonicalizeHeaderValue(l[i+1:])
            m[k] = v
        }
    }

    var out string
    for _, k := range keys {
        if v, ok := m[k]; ok {
            out += k + ":" + v + "\r\n"
        }
    }
    return out
}

func canonicalizeHeaderValue(v string) string {
    v = strings.TrimSpace(v)
    // collapse whitespace
    parts := strings.Fields(v)
    return strings.Join(parts, " ")
}

func LoadDKIMPrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
    block, _ := pem.Decode(pemBytes)
    if block == nil {
        return nil, fmt.Errorf("invalid PEM")
    }
    if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
        return key, nil
    }
    k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
    if err != nil {
        return nil, err
    }
    rsaKey, ok := k.(*rsa.PrivateKey)
    if !ok {
        return nil, fmt.Errorf("not RSA key")
    }
    return rsaKey, nil
}
