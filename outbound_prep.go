package main

import (
    "fmt"
    "strings"
    "time"
)

func prepareMessage(raw, from, to string) string {
    headers, body := splitHeaderBody(raw)

    h := map[string]string{}
    for _, line := range strings.Split(headers, "\r\n") {
        if i := strings.Index(line, ":"); i > 0 {
            k := strings.ToLower(strings.TrimSpace(line[:i]))
            v := strings.TrimSpace(line[i+1:])
            h[k] = v
        }
    }

    if h["date"] == "" {
        headers = "Date: " + time.Now().Format(time.RFC1123Z) + "\r\n" + headers
    }

    if h["message-id"] == "" {
        headers = fmt.Sprintf("Message-ID: <%d@mail.local>\r\n%s", time.Now().UnixNano(), headers)
    }

    if h["from"] == "" {
        headers = "From: " + from + "\r\n" + headers
    }

    if h["to"] == "" {
        headers = "To: " + to + "\r\n" + headers
    }

    if h["mime-version"] == "" {
        headers = "MIME-Version: 1.0\r\n" + headers
    }

    if h["content-type"] == "" {
        headers = "Content-Type: text/plain; charset=UTF-8\r\n" + headers
    }

    return headers + "\r\n" + body
}
