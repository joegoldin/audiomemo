#include <stddef.h>

typedef struct AMSystemAudio AMSystemAudio;
int am_system_audio_available(void);
AMSystemAudio *am_system_audio_start(double *rate, unsigned int *channels, char *error, size_t error_size);
// Returns bytes copied, or -1 if capture failed or the consumer fell behind.
int am_system_audio_read(AMSystemAudio *capture, void *data, size_t capacity);
void am_system_audio_stop(AMSystemAudio *capture);
void am_system_audio_free(AMSystemAudio *capture);
