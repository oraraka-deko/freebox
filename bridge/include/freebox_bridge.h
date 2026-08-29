#ifndef FREEBOX_BRIDGE_H
#define FREEBOX_BRIDGE_H

#include <stdint.h>
#include <stddef.h>
#include <stdbool.h>

#ifdef __cplusplus
extern "C" {
#endif

// FreeboxByteBuffer represents raw byte buffers (UTF-8 bytes or binary payloads).
typedef struct {
    const uint8_t* ptr;
    int32_t len;
} FreeboxByteBuffer;

// FreeboxRuneBuffer represents UTF-32 rune arrays (uint32_t codepoints).
typedef struct {
    const uint32_t* ptr;
    int32_t len;
} FreeboxRuneBuffer;

// Task Type codes (numeric byte enum)
typedef uint8_t FreeboxTaskType;
#define FREEBOX_TASK_CREATE  1
#define FREEBOX_TASK_DELETE  2
#define FREEBOX_TASK_COPY    3
#define FREEBOX_TASK_MOVE    4
#define FREEBOX_TASK_OPEN    5
#define FREEBOX_TASK_SERVE   6
#define FREEBOX_TASK_PROXY   7
#define FREEBOX_TASK_CUSTOM  8

// Task Status codes
typedef uint8_t FreeboxTaskStatus;
#define FREEBOX_STATUS_PENDING   0
#define FREEBOX_STATUS_RUNNING   1
#define FREEBOX_STATUS_PAUSED    2
#define FREEBOX_STATUS_COMPLETED 3
#define FREEBOX_STATUS_FAILED    4
#define FREEBOX_STATUS_CANCELED  5

// Server Type codes
typedef uint8_t FreeboxServerType;
#define FREEBOX_SERVER_API     1
#define FREEBOX_SERVER_HTTP    2
#define FREEBOX_SERVER_WEBDAV  3
#define FREEBOX_SERVER_FTP     4
#define FREEBOX_SERVER_SMB     5
#define FREEBOX_SERVER_SFTP    6
#define FREEBOX_SERVER_DLNA    7

// Archive Format codes
typedef uint8_t FreeboxArchiveFormat;
#define FREEBOX_ARCHIVE_ZIP    1
#define FREEBOX_ARCHIVE_TAR    2
#define FREEBOX_ARCHIVE_TGZ    3
#define FREEBOX_ARCHIVE_TBZ2   4
#define FREEBOX_ARCHIVE_RAR    5
#define FREEBOX_ARCHIVE_7Z     6

// Dedup Method codes
typedef uint8_t FreeboxDedupMethod;
#define FREEBOX_DEDUP_META     1
#define FREEBOX_DEDUP_QUICK    2
#define FREEBOX_DEDUP_MD5      3
#define FREEBOX_DEDUP_SHA256   4
#define FREEBOX_DEDUP_SHA1     5
#define FREEBOX_DEDUP_CRC32    6

// Dedup Action codes
typedef uint8_t FreeboxDedupAction;
#define FREEBOX_DEDUP_ACTION_REPORT 1
#define FREEBOX_DEDUP_ACTION_DELETE 2

// Dedup Keep Policy codes
typedef uint8_t FreeboxDedupKeepPolicy;
#define FREEBOX_DEDUP_KEEP_OLDEST   1
#define FREEBOX_DEDUP_KEEP_NEWEST   2
#define FREEBOX_DEDUP_KEEP_SHORTEST 3
#define FREEBOX_DEDUP_KEEP_FIRST    4

// Search Match Types
typedef uint8_t FreeboxSearchMatchType;
#define FREEBOX_SEARCH_MATCH_SUBSTRING 1
#define FREEBOX_SEARCH_MATCH_EXACT     2
#define FREEBOX_SEARCH_MATCH_REGEX     3
#define FREEBOX_SEARCH_MATCH_GLOB      4
#define FREEBOX_SEARCH_MATCH_PREFIX    5
#define FREEBOX_SEARCH_MATCH_SUFFIX    6

// Search Targets
typedef uint8_t FreeboxSearchTarget;
#define FREEBOX_SEARCH_TARGET_ALL      1
#define FREEBOX_SEARCH_TARGET_NAMES    2
#define FREEBOX_SEARCH_TARGET_CONTENT  3

// Task progress binary representation for FFI / CGO
typedef struct {
    uint64_t task_id;
    int64_t bytes_processed;
    int64_t total_bytes;
    double percent;
    double speed_bytes_sec;
    int64_t duration_ms;
    FreeboxTaskStatus status;
    FreeboxByteBuffer current_item;
    FreeboxByteBuffer error_msg;
} FreeboxTaskProgressC;

// Task record binary representation for historical query
typedef struct {
    uint64_t task_id;
    FreeboxTaskType task_type;
    FreeboxTaskStatus status;
    int32_t priority;
    int64_t bytes_processed;
    int64_t total_bytes;
    double percent;
    int64_t start_time_unix;
    int64_t end_time_unix;
    int64_t duration_ms;
    FreeboxByteBuffer src_path;
    FreeboxByteBuffer dst_path;
    FreeboxByteBuffer current_item;
    FreeboxByteBuffer error_msg;
} FreeboxTaskRecordC;

// File Info binary struct
typedef struct {
    FreeboxByteBuffer name;
    int64_t size;
    int64_t mod_time_unix;
    uint8_t is_dir;
    uint32_t mode;
} FreeboxFileInfoC;

// Mount Info binary struct
typedef struct {
    FreeboxByteBuffer name;
    FreeboxByteBuffer mount_type;
    FreeboxByteBuffer path;
    uint8_t read_only;
} FreeboxMountInfoC;

// Dedup progress binary struct
typedef struct {
    FreeboxByteBuffer phase;
    int64_t files_scanned;
    int64_t bytes_scanned;
    int64_t potential_groups;
    int64_t duplicate_groups;
    int64_t duplicate_files;
    int64_t duplicate_bytes;
    int64_t deleted_files;
    int64_t duration_ms;
    double percent;
    FreeboxByteBuffer current_path;
} FreeboxDedupProgressC;

// Search stats binary struct
typedef struct {
    int64_t files_scanned;
    int64_t dirs_scanned;
    int64_t files_matched;
    int64_t total_matches;
    int64_t replacements;
    int64_t bytes_read;
    int64_t duration_ms;
    double items_per_second;
    FreeboxByteBuffer current_path;
} FreeboxSearchStatsC;

// Media metadata binary struct
typedef struct {
    FreeboxByteBuffer title;
    FreeboxByteBuffer artist;
    FreeboxByteBuffer album;
    FreeboxByteBuffer format;
    int32_t width;
    int32_t height;
    int64_t duration_ms;
    int64_t bitrate;
} FreeboxMediaMetaC;

// Generic response struct
typedef struct {
    uint8_t success;
    FreeboxByteBuffer error_msg;
    uint64_t handle;
    int64_t value;
} FreeboxResultC;

#ifdef __cplusplus
}
#endif

#endif // FREEBOX_BRIDGE_H
