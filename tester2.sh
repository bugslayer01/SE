#!/bin/bash

# Color codes for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Configuration
BASE_URL="http://localhost:8080"
TEST_FILE_SIZE=$((500 * 1024 * 1024)) # 500 MB
CHUNK_SIZE=$((10 * 1024 * 1024)) # 10 MB chunks

# Helper functions
print_header() {
    echo ""
    echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${CYAN}$1${NC}"
    echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
}

print_success() {
    echo -e "${GREEN}✓ $1${NC}"
}

print_error() {
    echo -e "${RED}✗ $1${NC}"
}

print_info() {
    echo -e "${BLUE}ℹ $1${NC}"
}

print_warning() {
    echo -e "${YELLOW}⚠ $1${NC}"
}

# Cleanup function
cleanup() {
    print_info "Cleaning up test files..."
    rm -f test_file.bin
}

trap cleanup EXIT

# Test 1: Signup
print_header "Test 1: Creating new user account"
RANDOM_EMAIL="test_$(date +%s)@example.com"
SIGNUP_RESPONSE=$(curl -s -X POST "$BASE_URL/api/signup" \
    -H "Content-Type: application/json" \
    -d "{\"email\":\"$RANDOM_EMAIL\",\"password\":\"test123\"}")

if echo "$SIGNUP_RESPONSE" | grep -q "user created"; then
    print_success "User created: $RANDOM_EMAIL"
else
    print_error "Signup failed: $SIGNUP_RESPONSE"
    exit 1
fi

# Test 2: Login
print_header "Test 2: Login with credentials"
LOGIN_RESPONSE=$(curl -s -X POST "$BASE_URL/api/login" \
    -H "Content-Type: application/json" \
    -d "{\"email\":\"$RANDOM_EMAIL\",\"password\":\"test123\"}")

TOKEN=$(echo "$LOGIN_RESPONSE" | grep -o '"token":"[^"]*"' | cut -d'"' -f4)

if [ -z "$TOKEN" ]; then
    print_error "Login failed: $LOGIN_RESPONSE"
    exit 1
fi

print_success "Login successful"
print_info "JWT Token: ${TOKEN:0:20}..."

# Test 3: Add multiple Google Drive accounts
print_header "Test 3: Adding Google Drive Accounts"

# Ask user how many accounts to add
echo -e "${YELLOW}How many Google Drive accounts do you want to add? (1-5): ${NC}"
read NUM_ACCOUNTS

# Validate input
if ! [[ "$NUM_ACCOUNTS" =~ ^[1-5]$ ]]; then
    print_warning "Invalid input. Defaulting to 1 account."
    NUM_ACCOUNTS=1
fi

ACCOUNTS_ADDED=0

for ((i=1; i<=NUM_ACCOUNTS; i++)); do
    print_info "Adding Google Drive account $i of $NUM_ACCOUNTS..."

    # Get OAuth link
    OAUTH_RESPONSE=$(curl -s -X GET "$BASE_URL/api/drive/link" \
        -H "Authorization: Bearer $TOKEN")

    AUTH_URL=$(echo "$OAUTH_RESPONSE" | grep -o '"auth_url":"[^"]*"' | cut -d'"' -f4)

    if [ -z "$AUTH_URL" ]; then
        print_error "Failed to get OAuth URL: $OAUTH_RESPONSE"
        continue
    fi

    # Display OAuth URL
    echo ""
    echo -e "${YELLOW}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${YELLOW}ACCOUNT $i: Please authorize Google Drive access${NC}"
    echo -e "${YELLOW}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""
    echo -e "${CYAN}Open this URL in your browser:${NC}"
    echo "$AUTH_URL"
    echo ""
    echo -e "${YELLOW}Press ENTER after you've completed the authorization...${NC}"
    read -r

    # Verify account was added (with retry)
    RETRY_COUNT=0
    MAX_RETRIES=5
    ACCOUNT_VERIFIED=false

    while [ $RETRY_COUNT -lt $MAX_RETRIES ]; do
        sleep 2
        DRIVE_ACCOUNTS=$(curl -s -X GET "$BASE_URL/api/drive/accounts" \
            -H "Authorization: Bearer $TOKEN")

        CURRENT_COUNT=$(echo "$DRIVE_ACCOUNTS" | grep -o '"provider":"google"' | wc -l)

        if [ "$CURRENT_COUNT" -ge "$i" ]; then
            print_success "Account $i verified successfully"
            ACCOUNTS_ADDED=$((ACCOUNTS_ADDED + 1))
            ACCOUNT_VERIFIED=true
            break
        fi

        RETRY_COUNT=$((RETRY_COUNT + 1))
        print_warning "Waiting for account verification... (attempt $RETRY_COUNT/$MAX_RETRIES)"
    done

    if [ "$ACCOUNT_VERIFIED" = false ]; then
        print_error "Account $i verification failed after $MAX_RETRIES attempts"
        print_warning "Continuing with next account..."
    fi
done

echo ""
print_info "Total accounts added: $ACCOUNTS_ADDED"

if [ "$ACCOUNTS_ADDED" -eq 0 ]; then
    print_error "No accounts were added. Cannot proceed with upload test."
    exit 1
fi

# Test 4: Check drive spaces
print_header "Test 4: Checking available drive space"
DRIVE_SPACES=$(curl -s -X GET "$BASE_URL/api/drive/space" \
    -H "Authorization: Bearer $TOKEN")

NUM_DRIVES=$(echo "$DRIVE_SPACES" | grep -o '"account_id"' | wc -l)
print_success "Found $NUM_DRIVES drive(s) with available space"
echo "$DRIVE_SPACES" | python3 -m json.tool 2>/dev/null || echo "$DRIVE_SPACES"

# Test 5: Create test file
print_header "Test 5: Creating test file ($TEST_FILE_SIZE bytes)"
dd if=/dev/urandom of=test_file.bin bs=1M count=$((TEST_FILE_SIZE / 1024 / 1024)) 2>/dev/null
print_success "Test file created"

# Test 6: Initiate upload
print_header "Test 6: Initiating upload session"
INITIATE_RESPONSE=$(curl -s -X POST "$BASE_URL/api/files/upload/initiate" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"filename\":\"test_large_file.bin\",\"file_size\":$TEST_FILE_SIZE}")

SESSION_ID=$(echo "$INITIATE_RESPONSE" | grep -o '"session_id":"[^"]*"' | cut -d'"' -f4)

if [ -z "$SESSION_ID" ]; then
    print_error "Failed to initiate upload: $INITIATE_RESPONSE"
    exit 1
fi

print_success "Upload session initiated"
print_info "Session ID: $SESSION_ID"

# Test 7: Upload file in chunks
print_header "Test 7: Uploading file in chunks"

OFFSET=0
CHUNK_NUM=1
TOTAL_CHUNKS=$(( (TEST_FILE_SIZE + CHUNK_SIZE - 1) / CHUNK_SIZE ))

while [ $OFFSET -lt $TEST_FILE_SIZE ]; do
    # Extract chunk
    dd if=test_file.bin of=chunk.tmp bs=1 skip=$OFFSET count=$CHUNK_SIZE 2>/dev/null

    # Upload chunk
    UPLOAD_RESPONSE=$(curl -s -X POST "$BASE_URL/api/files/upload/chunk?session_id=$SESSION_ID" \
        -H "Authorization: Bearer $TOKEN" \
        -F "chunk=@chunk.tmp" \
        -F "offset=$OFFSET")

    PROGRESS=$(echo "$UPLOAD_RESPONSE" | grep -o '"progress":[0-9.]*' | cut -d':' -f2)

    printf "\r  Chunk %d/%d | Progress: %6.2f%%" $CHUNK_NUM $TOTAL_CHUNKS $PROGRESS

    OFFSET=$((OFFSET + CHUNK_SIZE))
    CHUNK_NUM=$((CHUNK_NUM + 1))

    rm -f chunk.tmp
done

echo ""
print_success "File upload complete"

# Test 8: Calculate chunking strategy
print_header "Test 8: Calculating chunk distribution (balanced)"
CHUNKING_RESPONSE=$(curl -s -X POST "$BASE_URL/api/files/chunking/calculate" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"file_size\":$TEST_FILE_SIZE,\"strategy\":\"balanced\"}")

NUM_FILE_CHUNKS=$(echo "$CHUNKING_RESPONSE" | grep -o '"num_chunks":[0-9]*' | cut -d':' -f2)
print_success "Chunking plan calculated: $NUM_FILE_CHUNKS chunks"
echo "$CHUNKING_RESPONSE" | python3 -m json.tool 2>/dev/null || echo "$CHUNKING_RESPONSE"

# Test 9: Finalize upload
print_header "Test 9: Finalizing upload and starting processing"
FINALIZE_RESPONSE=$(curl -s -X POST "$BASE_URL/api/files/upload/finalize" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"session_id\":\"$SESSION_ID\",\"strategy\":\"balanced\"}")

if echo "$FINALIZE_RESPONSE" | grep -q "processing started"; then
    print_success "Processing started"
else
    print_error "Finalize failed: $FINALIZE_RESPONSE"
    exit 1
fi

# Test 10: Monitor processing status (improved with timeout and better progress tracking)
print_header "Test 10: Monitoring processing status"

MAX_POLLS=120  # 10 minutes (5 seconds * 120)
POLL_COUNT=0
LAST_PROGRESS=0

while [ $POLL_COUNT -lt $MAX_POLLS ]; do
    sleep 5

    STATUS_RESPONSE=$(curl -s -X GET "$BASE_URL/api/files/upload/status/$SESSION_ID" \
        -H "Authorization: Bearer $TOKEN")

    STATUS=$(echo "$STATUS_RESPONSE" | grep -o '"status":"[^"]*"' | cut -d'"' -f4)
    PROGRESS=$(echo "$STATUS_RESPONSE" | grep -o '"processing_progress":[0-9.]*' | cut -d':' -f2)
    ERROR_MSG=$(echo "$STATUS_RESPONSE" | grep -o '"error_message":"[^"]*"' | cut -d'"' -f4)

    # Default progress to 0 if empty
    if [ -z "$PROGRESS" ]; then
        PROGRESS=0
    fi

    printf "\r  Status: %-12s | Progress: %6.1f%%" "$STATUS" "$PROGRESS"

    # Check if progress is stuck
    if (( $(echo "$PROGRESS == $LAST_PROGRESS" | bc -l) )) && [ "$STATUS" = "processing" ]; then
        if [ $POLL_COUNT -gt 5 ]; then  # Give it a few tries before warning
            print_warning "Progress appears stuck at $PROGRESS%"
        fi
    fi
    LAST_PROGRESS=$PROGRESS

    if [ "$STATUS" = "complete" ]; then
        echo ""
        print_success "Processing completed successfully!"
        break
    elif [ "$STATUS" = "failed" ]; then
        echo ""
        print_error "Processing failed: $ERROR_MSG"
        exit 1
    fi

    POLL_COUNT=$((POLL_COUNT + 1))
done

if [ $POLL_COUNT -ge $MAX_POLLS ]; then
    echo ""
    print_error "Processing timeout after 10 minutes"
    print_info "Final status: $STATUS ($PROGRESS%)"
    exit 1
fi

# Summary
echo ""
print_header "Test Summary"
print_success "All tests completed successfully!"
echo ""
print_info "User: $RANDOM_EMAIL"
print_info "Drive Accounts Added: $ACCOUNTS_ADDED"
print_info "Session ID: $SESSION_ID"
print_info "Test file size: $(numfmt --to=iec-i --suffix=B $TEST_FILE_SIZE)"
print_info "Chunks created: $NUM_FILE_CHUNKS"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
