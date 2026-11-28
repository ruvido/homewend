// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

// homewend-raw develops one camera raw file with LibRaw, as
// "dcraw_emu -w -o 4 -4 -q 2 -H 2" does: the white balance as shot,
// ProPhoto primaries, linear, 16 bits, no auto-brightening, PPG demosaic
// (one-pass for X-Trans), highlights blended; and not turned (-t 0), as the
// JPEG the camera embedded is not: the program reading both turns them alike.
//
// With -negative it decodes a film negative instead, as
// "dcraw_emu -4 -o 0 -r 1 1 1 1 -q 2 -H 0": the camera's own RGB, linear, no
// white balance, highlights clipped; the inversion is the engine's, and the
// camera matrix after it, which the header gives as rgb_cam. With -negative-half,
// at half the size (-h), for the analysis. With -fit=N, developed at half the
// size when the camera's picture is then still N pixels on its long side.
//
// Usage: homewend-raw [-fit=N | -negative | -negative-half] FILE
//
// It writes to stdout one line of JSON (size and metadata), then the pixels:
// width*height RGB triples of 16-bit samples in the machine's byte order,
// rows top to bottom, as the sensor has them. An error is a line on stderr
// and a non-zero exit.
#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cstdlib>
#include <string>

#include "libraw/libraw.h"

static std::string quote(const char *s)
{
  std::string out = "\"";
  for (; *s; s++)
  {
    unsigned char c = *s;
    if (c == '"' || c == '\\')
      out += '\\', out += c;
    else if (c < 0x20)
    {
      char esc[8];
      snprintf(esc, sizeof esc, "\\u%04x", c);
      out += esc;
    }
    else
      out += c;
  }
  return out + "\"";
}

static int fail(const char *what, int err)
{
  fprintf(stderr, "%s: %s\n", what, libraw_strerror(err));
  return 1;
}

int main(int argc, char **argv)
{
  std::string mode = argc == 3 ? argv[1] : "";
  int fit = mode.compare(0, 5, "-fit=") == 0 ? atoi(mode.c_str() + 5) : 0;
  if (argc < 2 || argc > 3 || (argc == 3 && fit <= 0 && mode != "-negative" && mode != "-negative-half"))
  {
    fprintf(stderr, "usage: homewend-raw [-fit=N | -negative | -negative-half] FILE\n");
    return 2;
  }
  const char *file = argv[argc - 1];
  static LibRaw raw; // static: the object is too big for the stack
  libraw_output_params_t &out = raw.imgdata.params;
  out.use_camera_wb = 1; // -w
  out.output_color = 4;  // -o 4, ProPhoto
  out.gamm[0] = out.gamm[1] = 1; // -4: linear, 16 bits, no auto-bright
  out.no_auto_bright = 1;
  out.output_bps = 16;
  out.user_qual = 2; // -q 2
  out.highlight = 2; // -H 2
  out.user_flip = 0;  // -t 0
  out.half_size = mode == "-negative-half"; // -h
  if (mode == "-negative" || mode == "-negative-half")
  {
    out.use_camera_wb = 0;
    out.output_color = 0; // -o 0, the camera's RGB
    for (int c = 0; c < 4; c++)
      out.user_mul[c] = 1; // -r 1 1 1 1
    out.highlight = 0;     // -H 0
  }
  // A file whose header lies about its size is refused, not given all the memory.
  raw.imgdata.rawparams.max_raw_memory_mb = 1024;

  int err;
  if ((err = raw.open_file(file)) != LIBRAW_SUCCESS)
    return fail("open", err);
  // As the engine reads it: no side past 65535, no more than 200 MP.
  const libraw_image_sizes_t &size = raw.imgdata.sizes;
  if (size.width > 65535 || size.height > 65535 || (double)size.width * size.height > 200e6)
  {
    fprintf(stderr, "open: %dx%d is too large\n", size.width, size.height);
    return 1;
  }
  // The camera's picture, smaller than the sensor's (Fuji), else the sensor's.
  const libraw_raw_inset_crop_t &crop = raw.imgdata.sizes.raw_inset_crops[0];
  int side = crop.cwidth && crop.cheight ? std::max(crop.cwidth, crop.cheight) : std::max(size.width, size.height);
  if (fit && side / 2 >= fit)
    out.half_size = 1; // -h
  if ((err = raw.unpack()) != LIBRAW_SUCCESS)
    return fail("unpack", err);
  // A half decode of an X-Trans sensor leaves one pixel in nine with no red
  // and no blue, and LibRaw fills them (pre_interpolate) where it finds the
  // first such pixel in the picture's top left corner. A black corner, as a
  // film holder's, has real pixels at 0 there: it finds the wrong ones,
  // leaves the holes and halves real samples, a film base 0.08 too dense in
  // red and blue. Its first 6 rows and 8 columns are lit to the white level.
  libraw_rawdata_t &rd = raw.imgdata.rawdata;
  if (raw.imgdata.params.half_size && raw.imgdata.idata.filters == 9 && rd.raw_image)
    for (int row = 0; row < 6; row++)
      for (int col = 0; col < 8; col++)
        rd.raw_image[(row + size.top_margin) * (size.raw_pitch / 2) + col + size.left_margin] =
            raw.imgdata.color.maximum;
  if ((err = raw.dcraw_process()) != LIBRAW_SUCCESS)
    return fail("process", err);
  libraw_processed_image_t *img = raw.dcraw_make_mem_image(&err);
  if (!img)
    return fail("image", err);
  if (img->type != LIBRAW_IMAGE_BITMAP || img->colors != 3 || img->bits != 16)
  {
    fprintf(stderr, "image: not 16-bit RGB\n");
    return 1;
  }

  const libraw_data_t &d = raw.imgdata;
  printf("{\"width\":%d,\"height\":%d", img->width, img->height);
  printf(",\"model\":%s", quote(d.idata.model).c_str());
  // Fuji's RawExposureBias and Canon's highlight tone priority, in stops.
  float shift = d.makernotes.common.ExposureCalibrationShift;
  printf(",\"exposure_shift\":%g", std::isfinite(shift) ? shift : 0.0f);
  // The size of the camera's own picture, smaller than the sensor's (Fuji).
  if (crop.cwidth && crop.cheight)
    printf(",\"frame\":{\"width\":%d,\"height\":%d}", crop.cwidth, crop.cheight);
  // Camera RGB to linear sRGB, rows as dcraw normalises them.
  printf(",\"rgb_cam\":[");
  for (int i = 0; i < 3; i++)
    printf("%s[%g,%g,%g]", i ? "," : "", d.color.rgb_cam[i][0], d.color.rgb_cam[i][1], d.color.rgb_cam[i][2]);
  printf("]}\n");
  bool written = fwrite(img->data, 1, img->data_size, stdout) == img->data_size;
  LibRaw::dcraw_clear_mem(img);
  if (!written || fflush(stdout) != 0)
  {
    perror("write");
    return 1;
  }
  return 0;
}
