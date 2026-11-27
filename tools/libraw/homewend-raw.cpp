// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

// homewend-raw develops one camera raw file with LibRaw, as
// "dcraw_emu -w -o 4 -4 -q 2 -H 2" does: the white balance as shot,
// ProPhoto primaries, linear, 16 bits, no auto-brightening, PPG demosaic
// (one-pass for X-Trans), highlights blended; and not turned (-t 0), as the
// JPEG the camera embedded is not: the program reading both turns them alike.
//
// Usage: homewend-raw FILE
//
// It writes to stdout one line of JSON (size and metadata), then the pixels:
// width*height RGB triples of 16-bit samples in the machine's byte order,
// rows top to bottom, as the sensor has them. An error is a line on stderr
// and a non-zero exit.
#include <cmath>
#include <cstdio>
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
  if (argc != 2)
  {
    fprintf(stderr, "usage: homewend-raw FILE\n");
    return 2;
  }
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
  // A file whose header lies about its size is refused, not given all the memory.
  raw.imgdata.rawparams.max_raw_memory_mb = 1024;

  int err;
  if ((err = raw.open_file(argv[1])) != LIBRAW_SUCCESS)
    return fail("open", err);
  // As the engine reads it: no side past 65535, no more than 200 MP.
  const libraw_image_sizes_t &size = raw.imgdata.sizes;
  if (size.width > 65535 || size.height > 65535 || (double)size.width * size.height > 200e6)
  {
    fprintf(stderr, "open: %dx%d is too large\n", size.width, size.height);
    return 1;
  }
  if ((err = raw.unpack()) != LIBRAW_SUCCESS)
    return fail("unpack", err);
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
  const libraw_raw_inset_crop_t &crop = d.sizes.raw_inset_crops[0];
  if (crop.cwidth && crop.cheight)
    printf(",\"frame\":{\"width\":%d,\"height\":%d}", crop.cwidth, crop.cheight);
  printf("}\n");
  bool written = fwrite(img->data, 1, img->data_size, stdout) == img->data_size;
  LibRaw::dcraw_clear_mem(img);
  if (!written || fflush(stdout) != 0)
  {
    perror("write");
    return 1;
  }
  return 0;
}
