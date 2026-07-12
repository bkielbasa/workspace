package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime    uint32 = 3
	argonMemory  uint32 = 64 * 1024
	argonThreads uint8  = 4
	argonKeyLen  uint32 = 32
	argonSaltLen uint32 = 16
)

func HashPassword(password string) (string, error) {
	if password == "" {
		return "", errors.New("password cannot be empty")
	}

	salt := make([]byte, argonSaltLen)

	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	hash := argon2.IDKey(
		[]byte(password),
		salt,
		argonTime,
		argonMemory,
		argonThreads,
		argonKeyLen,
	)

	encoded := fmt.Sprintf(
		"$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory,
		argonTime,
		argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	)

	return encoded, nil
}

func CheckPassword(encodedHash, password string) bool {
	params, salt, hash, err := decodePasswordHash(encodedHash)
	if err != nil {
		return false
	}

	otherHash := argon2.IDKey(
		[]byte(password),
		salt,
		params.time,
		params.memory,
		params.threads,
		params.keyLen,
	)

	return subtle.ConstantTimeCompare(hash, otherHash) == 1
}

type argonParams struct {
	time    uint32
	memory  uint32
	threads uint8
	keyLen  uint32
}

func decodePasswordHash(encoded string) (*argonParams, []byte, []byte, error) {
	var version int

	var memory uint32
	var time uint32
	var threads uint8

	var saltEncoded string
	var hashEncoded string

	_, err := fmt.Sscanf(
		encoded,
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		&version,
		&memory,
		&time,
		&threads,
		&saltEncoded,
		&hashEncoded,
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parse hash: %w", err)
	}

	if version != 19 {
		return nil, nil, nil, errors.New("unsupported argon2 version")
	}

	salt, err := base64.RawStdEncoding.DecodeString(saltEncoded)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("decode salt: %w", err)
	}

	hash, err := base64.RawStdEncoding.DecodeString(hashEncoded)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("decode hash: %w", err)
	}

	return &argonParams{
		time:    time,
		memory:  memory,
		threads: threads,
		keyLen:  uint32(len(hash)),
	}, salt, hash, nil
}
