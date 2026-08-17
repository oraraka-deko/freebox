#include "include/dart_api_dl.h"
#include "include/dart_api_dl.c"
#include "include/freebox_bridge.h"
#include <stdlib.h>
#include <string.h>

int32_t Freebox_InitDartApiDL(void* data) {
    return Dart_InitializeApiDL(data);
}

bool Freebox_PostTaskProgressToDart(
    int64_t port_id,
    uint64_t task_id,
    uint8_t status,
    int64_t bytes_processed,
    int64_t total_bytes,
    double percent,
    double speed_bytes_sec,
    int64_t duration_ms,
    const uint8_t* current_item_ptr,
    int32_t current_item_len,
    const uint8_t* error_ptr,
    int32_t error_len
) {
    if (port_id <= 0) return false;

    Dart_CObject c_task_id;
    c_task_id.type = Dart_CObject_kInt64;
    c_task_id.value.as_int64 = (int64_t)task_id;

    Dart_CObject c_status;
    c_status.type = Dart_CObject_kInt32;
    c_status.value.as_int32 = status;

    Dart_CObject c_bytes;
    c_bytes.type = Dart_CObject_kInt64;
    c_bytes.value.as_int64 = bytes_processed;

    Dart_CObject c_total;
    c_total.type = Dart_CObject_kInt64;
    c_total.value.as_int64 = total_bytes;

    Dart_CObject c_percent;
    c_percent.type = Dart_CObject_kDouble;
    c_percent.value.as_double = percent;

    Dart_CObject c_speed;
    c_speed.type = Dart_CObject_kDouble;
    c_speed.value.as_double = speed_bytes_sec;

    Dart_CObject c_duration;
    c_duration.type = Dart_CObject_kInt64;
    c_duration.value.as_int64 = duration_ms;

    Dart_CObject c_item;
    c_item.type = Dart_CObject_kTypedData;
    c_item.value.as_typed_data.type = Dart_TypedData_kUint8;
    c_item.value.as_typed_data.values = (uint8_t*)(current_item_len > 0 && current_item_ptr != NULL ? current_item_ptr : (const uint8_t*)"");
    c_item.value.as_typed_data.length = current_item_len > 0 ? current_item_len : 0;

    Dart_CObject c_error;
    c_error.type = Dart_CObject_kTypedData;
    c_error.value.as_typed_data.type = Dart_TypedData_kUint8;
    c_error.value.as_typed_data.values = (uint8_t*)(error_len > 0 && error_ptr != NULL ? error_ptr : (const uint8_t*)"");
    c_error.value.as_typed_data.length = error_len > 0 ? error_len : 0;

    Dart_CObject* values[9] = {
        &c_task_id,
        &c_status,
        &c_bytes,
        &c_total,
        &c_percent,
        &c_speed,
        &c_duration,
        &c_item,
        &c_error
    };

    Dart_CObject msg;
    msg.type = Dart_CObject_kArray;
    msg.value.as_array.length = 9;
    msg.value.as_array.values = values;

    return Dart_PostCObject_DL(port_id, &msg);
}
