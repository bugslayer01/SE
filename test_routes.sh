#!/bin/bash

# API Testing Script for Drive Backend
# Tests all routes with cURL

BASE_URL="http://localhost:8080"
EMAIL="testuser@example.com"
PASSWORD="testpass123"
TOKEN=""

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo "=========================================="
echo "  Drive Backend API Test Suite"
echo "=========================================="
echo ""

# Helper function to print test results
print_test() {
    if [ $1 -eq 0 ]; then
        echo -e "${GREEN}✓${NC} $2"
    else
        echo -e "${RED}✗${NC} $2"
    fi
}

# Helper function to print section headers
print_header() {
    echo ""
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${BLUE}  $1${NC}"
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""
}

# 1. Test Signup
print_header "1. Testing Signup (POST /api/signup)"

echo "Request:"
echo "POST $BASE_URL/api/signup"
echo "Body: {\"email\": \"$EMAIL\", \"password\": \"$PASSWORD\"}"
echo ""

SIGNUP_RESPONSE=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/api/signup" \
  -H "Content-Type: application/json" \
  -d "{\"email\": \"$EMAIL\", \"password\": \"$PASSWORD\"}")

HTTP_CODE=$(echo "$SIGNUP_RESPONSE" | tail -n1)
BODY=$(echo "$SIGNUP_RESPONSE" | sed '$d')

echo "Response Code: $HTTP_CODE"
echo "Response Body: $BODY"

if [ "$HTTP_CODE" -eq 201 ]; then
    print_test 0 "Signup successful"
elif [ "$HTTP_CODE" -eq 400 ] && [[ "$BODY" == *"email exists"* ]]; then
    echo -e "${YELLOW}⚠${NC} User already exists (expected if running multiple times)"
else
    print_test 1 "Signup failed with unexpected error"
fi

# 2. Test Signup with Duplicate Email
print_header "2. Testing Duplicate Signup (should fail)"

echo "Request:"
echo "POST $BASE_URL/api/signup (same email)"
echo ""

SIGNUP_DUP=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/api/signup" \
  -H "Content-Type: application/json" \
  -d "{\"email\": \"$EMAIL\", \"password\": \"$PASSWORD\"}")

HTTP_CODE=$(echo "$SIGNUP_DUP" | tail -n1)
BODY=$(echo "$SIGNUP_DUP" | sed '$d')

echo "Response Code: $HTTP_CODE"
echo "Response Body: $BODY"

if [ "$HTTP_CODE" -eq 400 ]; then
    print_test 0 "Duplicate email rejected correctly"
else
    print_test 1 "Should have rejected duplicate email"
fi

# 3. Test Signup with Short Password
print_header "3. Testing Signup with Short Password (should fail)"

echo "Request:"
echo "POST $BASE_URL/api/signup"
echo "Body: {\"email\": \"test2@example.com\", \"password\": \"123\"}"
echo ""

SHORT_PASS=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/api/signup" \
  -H "Content-Type: application/json" \
  -d "{\"email\": \"test2@example.com\", \"password\": \"123\"}")

HTTP_CODE=$(echo "$SHORT_PASS" | tail -n1)
BODY=$(echo "$SHORT_PASS" | sed '$d')

echo "Response Code: $HTTP_CODE"
echo "Response Body: $BODY"

if [ "$HTTP_CODE" -eq 400 ]; then
    print_test 0 "Short password rejected correctly"
else
    print_test 1 "Should have rejected short password"
fi

# 4. Test Login
print_header "4. Testing Login (POST /api/login)"

echo "Request:"
echo "POST $BASE_URL/api/login"
echo "Body: {\"email\": \"$EMAIL\", \"password\": \"$PASSWORD\"}"
echo ""

LOGIN_RESPONSE=$(curl -s -X POST "$BASE_URL/api/login" \
  -H "Content-Type: application/json" \
  -d "{\"email\": \"$EMAIL\", \"password\": \"$PASSWORD\"}")

echo "Response: $LOGIN_RESPONSE"

# Extract token using grep/sed/awk
TOKEN=$(echo "$LOGIN_RESPONSE" | grep -o '"token":"[^"]*"' | cut -d'"' -f4)

if [ -n "$TOKEN" ]; then
    print_test 0 "Login successful"
    echo "Token: ${TOKEN:0:20}..."
else
    print_test 1 "Login failed - no token received"
    echo -e "${RED}Cannot continue with protected route tests${NC}"
    exit 1
fi

# 5. Test Login with Wrong Password
print_header "5. Testing Login with Wrong Password (should fail)"

echo "Request:"
echo "POST $BASE_URL/api/login"
echo "Body: {\"email\": \"$EMAIL\", \"password\": \"wrongpass\"}"
echo ""

WRONG_LOGIN=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/api/login" \
  -H "Content-Type: application/json" \
  -d "{\"email\": \"$EMAIL\", \"password\": \"wrongpass\"}")

HTTP_CODE=$(echo "$WRONG_LOGIN" | tail -n1)
BODY=$(echo "$WRONG_LOGIN" | sed '$d')

echo "Response Code: $HTTP_CODE"
echo "Response Body: $BODY"

if [ "$HTTP_CODE" -eq 401 ]; then
    print_test 0 "Wrong password rejected correctly"
else
    print_test 1 "Should have rejected wrong password"
fi

# 6. Test Protected Route Without Token
print_header "6. Testing Protected Route Without Token (should fail)"

echo "Request:"
echo "GET $BASE_URL/api/drive/accounts (no Authorization header)"
echo ""

NO_AUTH=$(curl -s -w "\n%{http_code}" -X GET "$BASE_URL/api/drive/accounts")

HTTP_CODE=$(echo "$NO_AUTH" | tail -n1)
BODY=$(echo "$NO_AUTH" | sed '$d')

echo "Response Code: $HTTP_CODE"
echo "Response Body: $BODY"

if [ "$HTTP_CODE" -eq 401 ]; then
    print_test 0 "Unauthorized access blocked correctly"
else
    print_test 1 "Should have blocked unauthorized access"
fi

# 7. Test List Drive Accounts
print_header "7. Testing List Drive Accounts (GET /api/drive/accounts)"

echo "Request:"
echo "GET $BASE_URL/api/drive/accounts"
echo "Authorization: Bearer $TOKEN"
echo ""

ACCOUNTS_RESPONSE=$(curl -s -w "\n%{http_code}" -X GET "$BASE_URL/api/drive/accounts" \
  -H "Authorization: Bearer $TOKEN")

HTTP_CODE=$(echo "$ACCOUNTS_RESPONSE" | tail -n1)
BODY=$(echo "$ACCOUNTS_RESPONSE" | sed '$d')

echo "Response Code: $HTTP_CODE"
echo "Response Body: $BODY"

if [ "$HTTP_CODE" -eq 200 ]; then
    print_test 0 "List drive accounts successful"
else
    print_test 1 "Failed to list drive accounts"
fi

# 8. Test Drive Link (OAuth Start)
print_header "8. Testing Drive Link (GET /api/drive/link)"

echo "Request:"
echo "GET $BASE_URL/api/drive/link"
echo "Authorization: Bearer $TOKEN"
echo ""

LINK_RESPONSE=$(curl -s -w "\n%{http_code}" -X GET "$BASE_URL/api/drive/link" \
  -H "Authorization: Bearer $TOKEN")

HTTP_CODE=$(echo "$LINK_RESPONSE" | tail -n1)
BODY=$(echo "$LINK_RESPONSE" | sed '$d')

echo "Response Code: $HTTP_CODE"
echo "Response Body: $BODY"

if [ "$HTTP_CODE" -eq 200 ] && [[ "$BODY" == *"auth_url"* ]]; then
    print_test 0 "Drive link generation successful"
    
    # Extract and display OAuth URL
    AUTH_URL=$(echo "$BODY" | grep -o '"auth_url":"[^"]*"' | cut -d'"' -f4)
    echo ""
    echo -e "${YELLOW}OAuth URL:${NC}"
    echo "$AUTH_URL"
    echo ""
    echo -e "${YELLOW}Note:${NC} Open this URL in a browser to complete OAuth flow"
else
    print_test 1 "Failed to generate drive link"
fi

# 9. Test Method Not Allowed
print_header "9. Testing Method Restrictions"

echo "Request:"
echo "GET $BASE_URL/api/signup (should be POST only)"
echo ""

METHOD_TEST=$(curl -s -w "\n%{http_code}" -X GET "$BASE_URL/api/signup")

HTTP_CODE=$(echo "$METHOD_TEST" | tail -n1)
BODY=$(echo "$METHOD_TEST" | sed '$d')

echo "Response Code: $HTTP_CODE"
echo "Response Body: $BODY"

if [ "$HTTP_CODE" -eq 405 ]; then
    print_test 0 "Method restriction working correctly"
else
    print_test 1 "Should have rejected GET on signup"
fi

# 10. Test Invalid Token
print_header "10. Testing Invalid JWT Token"

echo "Request:"
echo "GET $BASE_URL/api/drive/accounts"
echo "Authorization: Bearer invalid_token_here"
echo ""

INVALID_TOKEN=$(curl -s -w "\n%{http_code}" -X GET "$BASE_URL/api/drive/accounts" \
  -H "Authorization: Bearer invalid_token_here")

HTTP_CODE=$(echo "$INVALID_TOKEN" | tail -n1)
BODY=$(echo "$INVALID_TOKEN" | sed '$d')

echo "Response Code: $HTTP_CODE"
echo "Response Body: $BODY"

if [ "$HTTP_CODE" -eq 401 ]; then
    print_test 0 "Invalid token rejected correctly"
else
    print_test 1 "Should have rejected invalid token"
fi

# Summary
print_header "Test Summary"

echo -e "${GREEN}✓${NC} All basic API tests completed!"
echo ""
echo "Notes:"
echo "- OAuth callback cannot be tested automatically (requires browser flow)"
echo "- To test OAuth: Use the auth_url from test #8 in a browser"
echo "- Ensure MongoDB is running and .env is configured correctly"
echo ""
echo "Your JWT Token (valid for 24 hours):"
echo "$TOKEN"
echo ""
echo "=========================================="
