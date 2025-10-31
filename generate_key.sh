#!/bin/bash

# Generate a 32-byte (256-bit) encryption key for TOKEN_ENC_KEY
# This script generates a cryptographically secure random key

echo "Generating 32-byte TOKEN_ENC_KEY..."
echo ""

# Method 1: Using openssl (most common)
if command -v openssl &> /dev/null; then
    KEY=$(openssl rand -base64 32)
    echo "Using openssl:"
    echo "TOKEN_ENC_KEY=$KEY"
    echo ""
fi

# Method 2: Using /dev/urandom and xxd
if command -v xxd &> /dev/null; then
    KEY=$(head -c 32 /dev/urandom | xxd -p -c 32)
    echo "Using xxd (hex format):"
    echo "TOKEN_ENC_KEY=$KEY"
    echo ""
fi

# Method 3: Using /dev/urandom and base64
KEY=$(head -c 32 /dev/urandom | base64)
echo "Using base64:"
echo "TOKEN_ENC_KEY=$KEY"
echo ""

echo "✓ Copy one of the above keys to your .env file"
echo "✓ The key must be exactly 32 bytes when decoded"
echo ""
echo "Quick add to .env file (using openssl method):"
echo "echo \"TOKEN_ENC_KEY=\$(openssl rand -base64 32)\" >> .env"
