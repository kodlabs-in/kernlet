//go:build darwin && cgo

#import "applevm_bridge.h"

#import <Foundation/Foundation.h>
#import <Virtualization/Virtualization.h>
#import <dispatch/dispatch.h>

#include <pthread.h>
#include <stdlib.h>
#include <string.h>

struct applevm_handle {
    VZVirtualMachine *vm;

    // Apple requires VM operations to happen on the
    // VM's serial dispatch queue.
    dispatch_queue_t queue;
};

typedef struct {
    pthread_mutex_t mutex;
    pthread_cond_t condition;

    int finished;
    char *error_message;
} applevm_waiter_t;

static void applevm_set_error(char **error_out, const char *message) {
    if (error_out == NULL) {
        return;
    }

    if (message == NULL) {
        message = "unknown applevm error";
    }

    *error_out = strdup(message);
}

static char *applevm_copy_nserror(NSError *error) {
    if (error == nil) {
        return NULL;
    }

    const char *message = [[error localizedDescription] UTF8String];

    if (message == NULL) {
        return strdup("unknown Apple Virtualization error");
    }

    return strdup(message);
}

static void applevm_waiter_init(applevm_waiter_t *waiter) {
    pthread_mutex_init(&waiter->mutex, NULL);
    pthread_cond_init(&waiter->condition, NULL);

    waiter->finished = 0;
    waiter->error_message = NULL;
}

static void applevm_waiter_finish(applevm_waiter_t *waiter, NSError *error) {
    char *message = applevm_copy_nserror(error);

    pthread_mutex_lock(&waiter->mutex);

    waiter->error_message = message;
    waiter->finished = 1;

    pthread_cond_signal(&waiter->condition);
    pthread_mutex_unlock(&waiter->mutex);
}


static void applevm_waiter_finish_message(applevm_waiter_t *waiter, const char *message) {
    pthread_mutex_lock(&waiter->mutex);

    waiter->error_message = strdup(message);
    waiter->finished = 1;

    pthread_cond_signal(&waiter->condition);
    pthread_mutex_unlock(&waiter->mutex);
}


static char *applevm_waiter_wait(applevm_waiter_t *waiter) {
    pthread_mutex_lock(&waiter->mutex);

    while (!waiter->finished) {
        pthread_cond_wait(&waiter->condition, &waiter->mutex);
    }

    char *error_message = waiter->error_message;

    pthread_mutex_unlock(&waiter->mutex);

    pthread_cond_destroy(&waiter->condition);
    pthread_mutex_destroy(&waiter->mutex);

    return error_message;
}

static VZVirtualMachineConfiguration *applevm_build_configuration(const applevm_config_t *config, NSError **error_out) {
    VZVirtualMachineConfiguration *configuration = [[[VZVirtualMachineConfiguration alloc] init] autorelease];

    [configuration setCPUCount:(NSUInteger)config->cpu_count];
    [configuration setMemorySize:config->memory_size];

    NSString *kernelPath = [NSString stringWithUTF8String:config->kernel_path];

    NSURL *kernelURL = [NSURL fileURLWithPath:kernelPath];

    VZLinuxBootLoader *bootLoader = [[[VZLinuxBootLoader alloc] initWithKernelURL:kernelURL] autorelease];

    NSString *initramfsPath = [NSString stringWithUTF8String:config->initramfs_path];

    NSURL *initramfsURL = [NSURL fileURLWithPath:initramfsPath];

    [bootLoader setInitialRamdiskURL:initramfsURL];

    NSString *commandLine = nil;

    if (config->kernel_command_line != NULL && config->kernel_command_line[0] != '\0') {
        commandLine = [NSString stringWithUTF8String:config->kernel_command_line];
    } else {
        commandLine = @"console=hvc0 root=/dev/vda rw";
    }

    [bootLoader setCommandLine:commandLine];

    [configuration setBootLoader:bootLoader];

    NSString *rootDiskPath = [NSString stringWithUTF8String:config->root_disk_path];

    NSURL *rootDiskURL = [NSURL fileURLWithPath:rootDiskPath];

    VZDiskImageStorageDeviceAttachment *diskAttachment = [[[VZDiskImageStorageDeviceAttachment alloc] initWithURL:rootDiskURL readOnly:NO error:error_out] autorelease];

    if (diskAttachment == nil) {
        return nil;
    }

    VZVirtioBlockDeviceConfiguration *blockDevice = [[[VZVirtioBlockDeviceConfiguration alloc] initWithAttachment:diskAttachment] autorelease];

    [configuration setStorageDevices:@[blockDevice]];

    VZVirtioConsoleDeviceSerialPortConfiguration *console = [[[VZVirtioConsoleDeviceSerialPortConfiguration alloc] init] autorelease];

    VZFileHandleSerialPortAttachment *stdio = [[[VZFileHandleSerialPortAttachment alloc] initWithFileHandleForReading:[NSFileHandle fileHandleWithStandardInput] fileHandleForWriting:[NSFileHandle fileHandleWithStandardOutput]] autorelease];

    [console setAttachment:stdio];

    [configuration setSerialPorts:@[console]];

    VZVirtioEntropyDeviceConfiguration *entropy = [[[VZVirtioEntropyDeviceConfiguration alloc] init] autorelease];

    [configuration setEntropyDevices:@[entropy]];

    return configuration;
}

applevm_handle_t *applevm_create(const applevm_config_t *config, char **error_out) {
    if (error_out != NULL) {
        *error_out = NULL;
    }

    if (config == NULL) {
        applevm_set_error(error_out, "config is NULL");

        return NULL;
    }

    @autoreleasepool {
        @try {
            NSError *error = nil;

            VZVirtualMachineConfiguration *configuration = applevm_build_configuration(config, &error);

            if (configuration == nil) {
                if (error != nil) {
                    char *message = applevm_copy_nserror(error);

                    if (error_out != NULL) {
                        *error_out = message;
                    } else {
                        free(message);
                    }
                }

                return NULL;
            }

            // Ask Apple whether this VM is valid.
            if (![configuration validateWithError:&error]) {
                char *message = applevm_copy_nserror(error);

                if (error_out != NULL) {
                    *error_out = message;
                } else {
                    free(message);
                }

                return NULL;
            }

            // Every VM gets its own serial queue.
            dispatch_queue_t queue = dispatch_queue_create("in.kodlabs.kernlet.applevm", DISPATCH_QUEUE_SERIAL);

            VZVirtualMachine *vm = [[VZVirtualMachine alloc] initWithConfiguration:configuration queue:queue];

            applevm_handle_t *handle = calloc(1, sizeof(applevm_handle_t));

            if (handle == NULL) {
                [vm release];
                dispatch_release(queue);

                applevm_set_error(error_out, "failed to allocate VM handle");

                return NULL;
            }

            handle->vm = vm;
            handle->queue = queue;

            return handle;
        } @catch (NSException *exception) {
            applevm_set_error(error_out, [[exception reason] UTF8String]);

            return NULL;
        }
    }
}

int applevm_start(applevm_handle_t *handle, char **error_out) {
    if (error_out != NULL) {
        *error_out = NULL;
    }

    if (handle == NULL || handle->vm == nil) {
        applevm_set_error(error_out, "invalid VM handle");

        return -1;
    }

    applevm_waiter_t waiter;
    applevm_waiter_init(&waiter);

    applevm_waiter_t *waiter_ptr = &waiter;
    VZVirtualMachine *vm = handle->vm;

    dispatch_async(handle->queue, ^{
        @autoreleasepool {
            if (![vm canStart]) {
                applevm_waiter_finish_message(waiter_ptr, "virtual machine cannot start in its current state");

                return;
            }


            [vm startWithCompletionHandler:^(NSError *error) {
                @autoreleasepool {
                    applevm_waiter_finish(waiter_ptr, error);
                }
            }];
        }
    });

    char *operation_error = applevm_waiter_wait(&waiter);

    if (operation_error != NULL) {
        if (error_out != NULL) {
            *error_out = operation_error;
        } else {
            free(operation_error);
        }

        return -1;
    }


    return 0;
}

int applevm_stop(applevm_handle_t *handle, char **error_out) {
    if (error_out != NULL) {
        *error_out = NULL;
    }

    if (handle == NULL || handle->vm == nil) {
        applevm_set_error(error_out, "invalid VM handle");

        return -1;
    }

    applevm_waiter_t waiter;
    applevm_waiter_init(&waiter);

    applevm_waiter_t *waiter_ptr = &waiter;
    VZVirtualMachine *vm = handle->vm;

    dispatch_async(handle->queue, ^{
        @autoreleasepool {
            if (![vm canStop]) {
                applevm_waiter_finish_message(waiter_ptr, "virtual machine cannot stop in its current state");

                return;
            }

            [vm stopWithCompletionHandler:^(NSError *error) {
                @autoreleasepool {
                    applevm_waiter_finish(waiter_ptr, error);
                }
            }];
        }
    });

    char *operation_error = applevm_waiter_wait(&waiter);

    if (operation_error != NULL) {
        if (error_out != NULL) {
            *error_out = operation_error;
        } else {
            free(operation_error);
        }

        return -1;
    }

    return 0;
}

void applevm_destroy(applevm_handle_t *handle) {
    if (handle == NULL) {
        return;
    }

    @autoreleasepool {
        [handle->vm release];

        dispatch_release(handle->queue);

        free(handle);
    }
}

void applevm_error_free(char *error_message) {
    free(error_message);
}
