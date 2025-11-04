# File Upload & Distribution API Documentation

## Overview

This API allows users to upload large files, inject obfuscation noise, split them into chunks, and distribute them across multiple Google Drive accounts.

## Flow Diagram

```
1. Initiate Upload → Get session ID & drive spaces
2. Upload File Chunks → Resumable upload to backend
3. Calculate Chunking → Choose strategy (optional)
4. Finalize Upload → Start processing
5. Poll Status → Check real-time progress
6. Download Key File → Get .2xpfm.key file
```

## Authentication

All endpoints (except OAuth callback) require JWT authentication:

```
Authorization: Bearer <your-jwt-token>
```

---

## Endpoints

### 1. Initiate Upload Session

**POST** `/api/files/upload/initiate`

Start a new file upload session and get available drive spaces.

**Request:**
```json
{
  "filename": "video.mp4",
  "file_size": 7516192768
}
```

**Response:**
```json
{
  "session_id": "507f1f77bcf86cd799439011",
  "upload_url": "/api/files/upload/chunk?session_id=507f1f77bcf86cd799439011",
  "drive_spaces": [
    {
      "account_id": "507f191e810c19729de860ea",
      "display_name": "Google Drive",
      "total_space": 17179869184,
      "used_space": 5368709120,
      "free_space": 11811160064,
      "available": true
    }
  ],
  "max_file_size": 107374182400
}
```

**Errors:**
- `400` - Invalid request or file size exceeds limit
- `500` - Server error or max concurrent uploads reached

---

### 2. Upload File Chunks

**POST** `/api/files/upload/chunk?session_id={session_id}`

Upload file data in chunks (resumable upload).

**Request:** `multipart/form-data`
- `chunk`: File data (binary)
- `offset`: Starting byte offset (integer)

**Example:**
```bash
curl -X POST "http://localhost:8080/api/files/upload/chunk?session_id=507f..." \
  -H "Authorization: Bearer <token>" \
  -F "chunk=@chunk_data.bin" \
  -F "offset=0"
```

**Response:**
```json
{
  "uploaded": 104857600,
  "total": 7516192768,
  "progress": 1.39
}
```

**Notes:**
- Upload chunks sequentially or in parallel
- Track offset to resume interrupted uploads
- Can upload in any chunk size

---

### 3. Calculate Chunking Strategy (Optional)

**POST** `/api/files/chunking/calculate`

Preview how file will be split before finalizing.

**Request:**
```json
{
  "file_size": 7516192768,
  "strategy": "balanced",
  "manual_chunk_sizes": []
}
```

**Strategies:**
- `greedy` - Fill largest drive first
- `balanced` - Equal distribution across drives
- `proportional` - Proportional to available space
- `manual` - User-defined sizes (requires `manual_chunk_sizes`)

**Response:**
```json
{
  "plan": [
    {
      "chunk_id": 1,
      "drive_account_id": "507f...",
      "size": 2505730922,
      "start_offset": 0,
      "end_offset": 2505730922
    },
    {
      "chunk_id": 2,
      "drive_account_id": "507f...",
      "size": 2505730922,
      "start_offset": 2505730922,
      "end_offset": 5011461844
    }
  ],
  "num_chunks": 3
}
```

---

### 4. Finalize Upload

**POST** `/api/files/upload/finalize`

Start processing: obfuscate, chunk, and upload to drives.

**Request:**
```json
{
  "session_id": "507f1f77bcf86cd799439011",
  "strategy": "balanced",
  "manual_chunk_sizes": []
}
```

**Response:**
```json
{
  "message": "processing started",
  "session_id": "507f1f77bcf86cd799439011",
  "status_url": "/api/files/upload/status/507f1f77bcf86cd799439011"
}
```

**Notes:**
- Upload must be 100% complete before finalizing
- Processing happens asynchronously
- Poll status endpoint for progress

---

### 5. Check Upload Status

**GET** `/api/files/upload/status/{session_id}`

Get real-time processing status.

**Response:**
```json
{
  "status": "processing",
  "uploaded_size": 7516192768,
  "total_size": 7516192768,
  "processing_progress": 75.5,
  "error_message": "",
  "completed_at": null
}
```

**Status Values:**
- `uploading` - File still being uploaded
- `processing` - Obfuscating, chunking, uploading to drives
- `complete` - Successfully completed
- `failed` - Error occurred (see `error_message`)

**Processing Steps:**
- 10% - Injecting noise
- 20% - Checking drive spaces
- 30% - Calculating chunk distribution
- 50% - Splitting file into chunks
- 70-90% - Uploading chunks to drives
- 95% - Generating key file
- 100% - Complete

---

### 6. Get Drive Spaces

**GET** `/api/drive/space`

Get available space on all linked Google Drive accounts.

**Response:**
```json
[
  {
    "account_id": "507f191e810c19729de860ea",
    "display_name": "Google Drive",
    "total_space": 17179869184,
    "used_space": 5368709120,
    "free_space": 11811160064,
    "available": true
  },
  {
    "account_id": "507f1f77bcf86cd799439012",
    "display_name": "Google Drive",
    "total_space": 10737418240,
    "used_space": 9663676416,
    "free_space": 1073741824,
    "available": true,
    "error": ""
  }
]
```

---

## Complete Upload Flow Example

```javascript
// 1. Initiate upload
const initiateRes = await fetch('/api/files/upload/initiate', {
  method: 'POST',
  headers: {
    'Authorization': `Bearer ${token}`,
    'Content-Type': 'application/json'
  },
  body: JSON.stringify({
    filename: 'large_video.mp4',
    file_size: file.size
  })
});

const { session_id, drive_spaces } = await initiateRes.json();

// 2. Upload file in chunks
const chunkSize = 10 * 1024 * 1024; // 10MB chunks
for (let offset = 0; offset < file.size; offset += chunkSize) {
  const chunk = file.slice(offset, offset + chunkSize);
  
  const formData = new FormData();
  formData.append('chunk', chunk);
  formData.append('offset', offset);
  
  await fetch(`/api/files/upload/chunk?session_id=${session_id}`, {
    method: 'POST',
    headers: { 'Authorization': `Bearer ${token}` },
    body: formData
  });
}

// 3. (Optional) Calculate chunking
const chunkingRes = await fetch('/api/files/chunking/calculate', {
  method: 'POST',
  headers: {
    'Authorization': `Bearer ${token}`,
    'Content-Type': 'application/json'
  },
  body: JSON.stringify({
    file_size: file.size,
    strategy: 'balanced'
  })
});

const { plan } = await chunkingRes.json();
console.log('Chunks will be distributed:', plan);

// 4. Finalize upload
const finalizeRes = await fetch('/api/files/upload/finalize', {
  method: 'POST',
  headers: {
    'Authorization': `Bearer ${token}`,
    'Content-Type': 'application/json'
  },
  body: JSON.stringify({
    session_id: session_id,
    strategy: 'balanced'
  })
});

// 5. Poll status
const statusUrl = `/api/files/upload/status/${session_id}`;
const pollInterval = setInterval(async () => {
  const statusRes = await fetch(statusUrl, {
    headers: { 'Authorization': `Bearer ${token}` }
  });
  
  const status = await statusRes.json();
  console.log(`Progress: ${status.processing_progress}%`);
  
  if (status.status === 'complete') {
    clearInterval(pollInterval);
    console.log('Upload complete! Download key file.');
  } else if (status.status === 'failed') {
    clearInterval(pollInterval);
    console.error('Upload failed:', status.error_message);
  }
}, 2000); // Poll every 2 seconds
```

---

## Key File Format

After successful upload, user receives a `.2xpfm.key` file:

```json
{
  "version": "1.0",
  "original_filename": "video.mp4",
  "original_size": 7516192768,
  "processed_size": 8117328189,
  "obfuscation": {
    "algorithm": "ChaCha20-DRBG",
    "seed": "base64_encoded_32_bytes",
    "block_size": 256,
    "overhead_pct": 8.0,
    "min_gap": 4096
  },
  "chunks": [
    {
      "chunk_id": 1,
      "drive_account_id": "507f191e810c19729de860ea",
      "drive_file_id": "1abc_google_drive_file_id",
      "filename": "chunk_001.2xpfm",
      "start_offset": 0,
      "end_offset": 2505730922,
      "size": 2505730922,
      "checksum": "sha256_hash"
    }
  ],
  "created_at": "2024-11-04T10:30:00Z"
}
```

**Important:**
- Key file is NEVER stored on server
- User must download and save it securely
- Required for file reconstruction/download

---

## Error Handling

### Common Errors:

**400 Bad Request**
- Invalid JSON
- Missing required fields
- File size exceeds limit
- Insufficient drive space

**401 Unauthorized**
- Missing or invalid JWT token
- Session expired

**500 Internal Server Error**
- MongoDB connection failed
- Google Drive API error
- File processing error

### Error Response Format:
```json
{
  "error": "descriptive error message"
}
```

---

## Rate Limits & Constraints

| Constraint | Default | Configurable |
|------------|---------|--------------|
| Max file size | 100 GB | `MAX_FILE_SIZE_GB` |
| Session expiry | 1 hour | `SESSION_EXPIRY_HOURS` |
| Max concurrent uploads per user | 1 | `MAX_CONCURRENT_UPLOADS_PER_USER` |
| Temp file cleanup | 10 minutes after completion | `TEMP_FILE_CLEANUP_MINUTES` |
| Obfuscation block size | 256 bytes | `OBFUSCATION_BLOCK_SIZE` |
| Noise overhead | ~8% | `OBFUSCATION_OVERHEAD_PCT` |

---

## Best Practices

### For Frontend:

1. **Chunk Size**: Use 5-10 MB chunks for optimal performance
2. **Progress Tracking**: Update UI with `processing_progress` value
3. **Error Recovery**: Implement retry logic for failed chunk uploads
4. **Key File Storage**: Prompt user to save key file immediately
5. **Polling**: Poll status every 2-5 seconds during processing

### For Backend:

1. **Space Check**: Always verify drive space before upload
2. **Cleanup**: Temp files auto-delete after configured duration
3. **Monitoring**: Check logs for failed uploads
4. **Sessions**: Clean up expired sessions regularly

---

## cURL Examples

```bash
# 1. Login
TOKEN=$(curl -s -X POST http://localhost:8080/api/login \
  -H "Content-Type: application/json" \
  -d '{"email":"test@example.com","password":"test123"}' \
  | jq -r '.token')

# 2. Check drive spaces
curl -X GET http://localhost:8080/api/drive/space \
  -H "Authorization: Bearer $TOKEN"

# 3. Initiate upload
SESSION=$(curl -s -X POST http://localhost:8080/api/files/upload/initiate \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"filename":"test.mp4","file_size":1073741824}' \
  | jq -r '.session_id')

# 4. Upload chunk
curl -X POST "http://localhost:8080/api/files/upload/chunk?session_id=$SESSION" \
  -H "Authorization: Bearer $TOKEN" \
  -F "chunk=@file_chunk.bin" \
  -F "offset=0"

# 5. Finalize
curl -X POST http://localhost:8080/api/files/upload/finalize \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"session_id\":\"$SESSION\",\"strategy\":\"balanced\"}"

# 6. Check status
curl -X GET "http://localhost:8080/api/files/upload/status/$SESSION" \
  -H "Authorization: Bearer $TOKEN"
```

---

## Security Notes

1. **JWT Tokens**: Expire after 24 hours
2. **OAuth Tokens**: Encrypted with AES-256-GCM
3. **Obfuscation Seed**: 256-bit CSPRNG
4. **Temp Files**: Isolated per user, auto-cleanup
5. **Key Files**: Never stored on server
6. **Drive Access**: OAuth 2.0 with offline access

---

## Support

For issues or questions, check server logs for detailed error messages.