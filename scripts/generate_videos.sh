#!/bin/bash

# Generate 256-color palette of minimal AV1 videos for embedding placeholders

set -euo pipefail

OUTPUT_DIR="./template_videos/"
TAR_FILE="./template_videos.tar.gz"
DIM=128

echo "Generating palette of 256 ${DIM}x${DIM} AV1 videos..."

# Clean up any existing palette
if [[ -d "$OUTPUT_DIR" ]]; then rm -r -- "$OUTPUT_DIR"; fi
mkdir -p -- "$OUTPUT_DIR"

# Generate 216 colors from 6x6x6 RGB cube
count=0
for r in 0 51 102 153 204 255; do
    for g in 0 51 102 153 204 255; do
        for b in 0 51 102 153 204 255; do
            color=$(printf "0x%02x%02x%02x" $r $g $b)
            color_g=$(printf "0x%02x%02x%02x" $(( (r+40)%256 )) $(( (g+40)%256 )) $(( (b+40)%256 )))
            output="${OUTPUT_DIR}/$(printf '%03d' $count).mp4"

            ffmpeg -f lavfi \
                -i "gradients=n=2:type=linear:speed=0:x0=10:y0=10:x1=$((DIM-10)):y1=$((DIM-10)):c0=${color}:c1=${color_g}:s=${DIM}x${DIM}:d=3" \
                -c:v libaom-av1 -cpu-used 0 -pix_fmt yuv420p \
                -crf 63 -b:v 0 \
                -flags:v +bitexact -fflags +bitexact -movflags +faststart \
                -an "$output" -y \
                > /dev/null 2>&1

#            ffmpeg -f lavfi -i "color=c=${color}:s=128x128:d=3" \
#                -c:v libaom-av1 -cpu-used 0 -pix_fmt yuv420p \
#                -crf 63 -b:v 0 \
#                -flags:v +bitexact -fflags +bitexact -movflags +faststart \
#                -an "$output" -y \
#                > /dev/null 2>&1

            count=$((count + 1))
            printf .
            if [[ $((count % 64)) -eq 0 ]]; then
                echo "  ($count/256)"
            fi
        done
    done
done

# Generate 40 grayscale colors
for i in $(seq 0 39); do
    val=$((i * 255 / 39))
    val_g=$(( (val+40)%256 ))
    color=$(printf "0x%02x%02x%02x" $val $val $val)
    color_g=$(printf "0x%02x%02x%02x" $val_g $val_g $val_g)
    output="${OUTPUT_DIR}/$(printf '%03d' $count).mp4"

    ffmpeg -f lavfi \
        -i "gradients=n=2:type=linear:speed=0:x0=10:y0=10:x1=$((DIM-10)):y1=$((DIM-10)):c0=${color}:c1=${color_g}:s=${DIM}x${DIM}:d=3" \
        -c:v libaom-av1 -cpu-used 0 -pix_fmt yuv420p \
        -crf 63 -b:v 0 \
        -flags:v +bitexact -fflags +bitexact -movflags +faststart \
        -an "$output" -y \
        > /dev/null 2>&1

#    ffmpeg -f lavfi -i "color=c=${color}:s=128x128:d=3" \
#        -c:v libaom-av1 -cpu-used 0 -pix_fmt yuv420p \
#        -crf 63 -b:v 0 \
#        -flags:v +bitexact -fflags +bitexact -movflags +faststart \
#        -an "$output" -y \
#        > /dev/null 2>&1

    count=$((count + 1))
    printf .
    if [[ $((count % 64)) -eq 0 ]]; then
        echo "  ($count/256)"
    fi
done
if [[ $((count % 64)) -ne 0 ]]; then
    echo "  ($count/256)"
fi
echo 'Creating tar archive...'

# Create tar archive with reproducible settings (no timestamps, owners, etc.)
tar --owner=0 --group=0 \
    --numeric-owner \
    --mtime='@0' \
    --sort=name \
    --format=gnu \
    -cf - template_videos/ | gzip -n -9 > "$TAR_FILE"

echo "Complete. Output: $TAR_FILE"
