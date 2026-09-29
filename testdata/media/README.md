Winzige Testvideos (je ein schwarzes Bild, Stille), erzeugt mit ffmpeg 7.0:

```sh
ffmpeg -f lavfi -i color=c=black:s=3840x1600:r=24:d=0.042 -f lavfi -i anullsrc=cl=5.1:r=48000 -f lavfi -i anullsrc=cl=stereo:r=48000 \
  -map 0 -map 1 -map 2 -t 0.042 -c:v libx265 -pix_fmt yuv420p10le -color_primaries bt2020 -color_trc smpte2084 -colorspace bt2020nc \
  -c:a:0 eac3 -c:a:1 aac -metadata:s:a:0 language=ger -metadata:s:a:1 language=eng -disposition:a:1 0 hevc-hdr10.mkv
ffmpeg -f lavfi -i color=c=black:s=1920x800:r=24:d=0.042 -f lavfi -i anullsrc=cl=5.1:r=48000 -f lavfi -i anullsrc=cl=stereo:r=48000 \
  -map 0 -map 1 -map 2 -t 0.042 -c:v libx264 -c:a:0 ac3 -c:a:1 aac -metadata:s:a:0 language=deu -metadata:s:a:1 language=eng h264-scope.mp4
ffmpeg -f lavfi -i color=c=black:s=1280x720:r=24:d=0.042 -f lavfi -i anullsrc=cl=stereo:r=48000 -t 0.042 -c:v libaom-av1 -cpu-used 8 -c:a libopus av1.mkv
ffmpeg -f lavfi -i color=c=black:s=1920x1080:r=24:d=0.042 -f lavfi -i anullsrc=cl=5.1:r=48000 -t 0.042 -c:v libx265 -tag:v hvc1 \
  -color_trc arib-std-b67 -color_primaries bt2020 -colorspace bt2020nc -c:a eac3 -metadata:s:a:0 language=eng -movflags +faststart hevc-hlg.mp4
ffmpeg -f lavfi -i color=c=black:s=720x576:r=25:d=0.04 -f lavfi -i anullsrc=cl=stereo:r=48000 -t 0.04 -c:v mpeg4 -c:a ac3 dvd.avi
```

`h264-scope.mp4` hat die moov-Box am Ende (hinter den Filmdaten), `hevc-hlg.mp4` am Anfang.
