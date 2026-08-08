#ifndef KERNLET_APPLEVM_BRIDGE_H
#define KERNLET_APPLEVM_BRIDGE_H

#include <stdint.h>

// Go can hold this pointer,
// but it does NOT know what is inside it.
typedef struct applevm_handle applevm_handle_t;

// Plain C version of our Go Config.
typedef struct {
    const char *kernel_path;
    const char *initramfs_path;
    const char *root_disk_path;
    const char *kernel_command_line;

    uint32_t cpu_count;
    uint64_t memory_size;
} applevm_config_t;

// Create the VM.
//
// Returns:
//   VM handle on success
//   NULL on failure
//
// If it fails, error_out receives an error message.
applevm_handle_t *applevm_create(const applevm_config_t *config, char **error_out);

// Start the VM.
//
// 0  = success
// -1  = failure
int applevm_start(applevm_handle_t *vm, char **error_out);

// Stop the VM.
int applevm_stop(applevm_handle_t *vm, char **error_out);

// Release everything belonging to the VM.
void applevm_destroy(applevm_handle_t *vm);

// Free error strings created by Objective-C/C.
void applevm_error_free(char *error_message);

#endif
