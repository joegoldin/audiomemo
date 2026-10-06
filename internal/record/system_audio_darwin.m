//go:build darwin && cgo

#import <CoreAudio/CoreAudio.h>
#import <CoreAudio/AudioHardwareTapping.h>
#import <CoreAudio/CATapDescription.h>
#import <Foundation/Foundation.h>
#include <stdatomic.h>
#include <stdio.h>
#include "system_audio_darwin.h"

#define AM_RING_SAMPLES (1u << 20)

struct AMSystemAudio {
    AudioObjectID tap;
    AudioObjectID device;
    AudioDeviceIOProcID proc;
    unsigned int channels;
    _Atomic unsigned long long read_index;
    _Atomic unsigned long long write_index;
    _Atomic int failed;
    float ring[AM_RING_SAMPLES];
};

int am_system_audio_available(void) {
    if (@available(macOS 14.2, *)) return 1;
    return 0;
}

static OSStatus capture_audio(AudioDeviceID device, const AudioTimeStamp *now,
                              const AudioBufferList *input, const AudioTimeStamp *input_time,
                              AudioBufferList *output, const AudioTimeStamp *output_time,
                              void *context) {
    AMSystemAudio *capture = context;
    if (!input || input->mNumberBuffers == 0) return noErr;
    unsigned int channels = 0;
    unsigned int frames = 0;
    for (UInt32 i = 0; i < input->mNumberBuffers; i++) {
        const AudioBuffer *buffer = &input->mBuffers[i];
        if (!buffer->mNumberChannels || !buffer->mData) return noErr;
        unsigned int count = buffer->mDataByteSize / (sizeof(float) * buffer->mNumberChannels);
        if (i == 0) frames = count;
        if (count != frames) {
            atomic_store(&capture->failed, 1);
            return noErr;
        }
        channels += buffer->mNumberChannels;
    }
    if (channels != capture->channels) {
        atomic_store(&capture->failed, 1);
        return noErr;
    }
    unsigned long long write_index = atomic_load_explicit(&capture->write_index, memory_order_relaxed);
    unsigned long long read_index = atomic_load_explicit(&capture->read_index, memory_order_acquire);
    unsigned long long samples = (unsigned long long)frames * channels;
    if (samples > AM_RING_SAMPLES - (write_index - read_index)) {
        atomic_store(&capture->failed, 1);
        return noErr;
    }
    // The real-time callback never allocates, locks, or waits for the encoder.
    for (unsigned int frame = 0; frame < frames; frame++) {
        for (UInt32 b = 0; b < input->mNumberBuffers; b++) {
            const AudioBuffer *buffer = &input->mBuffers[b];
            const float *data = buffer->mData;
            for (UInt32 channel = 0; channel < buffer->mNumberChannels; channel++) {
                capture->ring[write_index++ % AM_RING_SAMPLES] = data[frame * buffer->mNumberChannels + channel];
            }
        }
    }
    atomic_store_explicit(&capture->write_index, write_index, memory_order_release);
    return noErr;
}

AMSystemAudio *am_system_audio_start(double *rate, unsigned int *channels, char *error, size_t error_size) {
    if (@available(macOS 14.2, *)) {
        @autoreleasepool {
            AMSystemAudio *capture = calloc(1, sizeof(*capture));
            if (!capture) {
                snprintf(error, error_size, "cannot allocate system audio buffer");
                return NULL;
            }
            atomic_init(&capture->read_index, 0);
            atomic_init(&capture->write_index, 0);
            atomic_init(&capture->failed, 0);
            CATapDescription *description = [[CATapDescription alloc] initStereoGlobalTapButExcludeProcesses:@[]];
            description.name = @"Audiomemo system audio";
            description.privateTap = YES;
            description.muteBehavior = CATapUnmuted;
            NSDictionary *aggregate = nil;
            OSStatus status = AudioHardwareCreateProcessTap(description, &capture->tap);
            const char *stage = "create process tap";
            if (status != noErr) goto failed;

            AudioStreamBasicDescription format;
            UInt32 size = sizeof(format);
            AudioObjectPropertyAddress address = {kAudioTapPropertyFormat, kAudioObjectPropertyScopeGlobal, kAudioObjectPropertyElementMain};
            stage = "read tap format";
            status = AudioObjectGetPropertyData(capture->tap, &address, 0, NULL, &size, &format);
            if (status != noErr) goto failed;
            if (format.mFormatID != kAudioFormatLinearPCM || !(format.mFormatFlags & kAudioFormatFlagIsFloat) ||
                (format.mFormatFlags & kAudioFormatFlagIsBigEndian) || format.mBitsPerChannel != 32 ||
                format.mChannelsPerFrame != 2 || format.mSampleRate <= 0) {
                snprintf(error, error_size, "unsupported Core Audio tap format (expected stereo float32 PCM)");
                am_system_audio_free(capture);
                return NULL;
            }
            capture->channels = format.mChannelsPerFrame;
            *channels = capture->channels;
            *rate = format.mSampleRate;

            aggregate = @{
                @kAudioAggregateDeviceNameKey: @"Audiomemo system audio",
                @kAudioAggregateDeviceUIDKey: [[NSUUID UUID] UUIDString],
                @kAudioAggregateDeviceIsPrivateKey: @YES,
                @kAudioAggregateDeviceTapAutoStartKey: @YES,
                @kAudioAggregateDeviceTapListKey: @[@{
                    @kAudioSubTapUIDKey: description.UUID.UUIDString,
                    @kAudioSubTapDriftCompensationKey: @YES
                }]
            };
            stage = "create tap aggregate device";
            status = AudioHardwareCreateAggregateDevice((__bridge CFDictionaryRef)aggregate, &capture->device);
            if (status != noErr) goto failed;
            stage = "create audio callback";
            status = AudioDeviceCreateIOProcID(capture->device, capture_audio, capture, &capture->proc);
            if (status != noErr) goto failed;
            stage = "start system audio capture";
            status = AudioDeviceStart(capture->device, capture->proc);
            if (status != noErr) goto failed;
            return capture;

        failed:
            snprintf(error, error_size, "%s: Core Audio status %d. Allow audio recording in System Settings > Privacy & Security > Screen & System Audio Recording", stage, (int)status);
            am_system_audio_free(capture);
            return NULL;
        }
    }
    snprintf(error, error_size, "system audio capture requires macOS 14.2 or later");
    return NULL;
}

int am_system_audio_read(AMSystemAudio *capture, void *data, size_t capacity) {
    if (atomic_load(&capture->failed)) return -1;
    unsigned long long read_index = atomic_load_explicit(&capture->read_index, memory_order_relaxed);
    unsigned long long write_index = atomic_load_explicit(&capture->write_index, memory_order_acquire);
    size_t count = (write_index - read_index);
    if (count > capacity / sizeof(float)) count = capacity / sizeof(float);
    float *output = data;
    for (size_t i = 0; i < count; i++) output[i] = capture->ring[(read_index + i) % AM_RING_SAMPLES];
    atomic_store_explicit(&capture->read_index, read_index + count, memory_order_release);
    return (int)(count * sizeof(float));
}

void am_system_audio_stop(AMSystemAudio *capture) {
    if (capture->proc) {
        AudioDeviceStop(capture->device, capture->proc);
        AudioDeviceDestroyIOProcID(capture->device, capture->proc);
        capture->proc = NULL;
    }
}

void am_system_audio_free(AMSystemAudio *capture) {
    am_system_audio_stop(capture);
    if (capture->device) AudioHardwareDestroyAggregateDevice(capture->device);
    if (@available(macOS 14.2, *)) {
        if (capture->tap) AudioHardwareDestroyProcessTap(capture->tap);
    }
    free(capture);
}
