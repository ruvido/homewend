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

  int err;
  if ((err = raw.open_file(argv[1])) != LIBRAW_SUCCESS)
    return fail("open", err);
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

  // The largest embedded JPEG, read straight from the file by whoever wants it.
  const libraw_thumbnail_item_t *preview = nullptr;
  const libraw_thumbnail_list_t &thumbs = raw.imgdata.thumbs_list;
  for (int i = 0; i < thumbs.thumbcount; i++)
  {
    const libraw_thumbnail_item_t &t = thumbs.thumblist[i];
    if (t.tformat == LIBRAW_INTERNAL_THUMBNAIL_JPEG &&
        (!preview || t.twidth * t.theight > preview->twidth * preview->theight))
      preview = &t;
  }

  const libraw_data_t &d = raw.imgdata;
  printf("{\"width\":%d,\"height\":%d", img->width, img->height);
  printf(",\"make\":%s,\"model\":%s,\"lens\":%s", quote(d.idata.make).c_str(),
         quote(d.idata.model).c_str(), quote(d.lens.Lens).c_str());
  printf(",\"iso\":%g", d.other.iso_speed);
  printf(",\"wb\":[%g,%g,%g,%g]", d.color.cam_mul[0], d.color.cam_mul[1],
         d.color.cam_mul[2], d.color.cam_mul[3]);
  // Fuji's RawExposureBias and Canon's highlight tone priority, in stops.
  printf(",\"exposure_shift\":%g", d.makernotes.common.ExposureCalibrationShift);
  printf(",\"baseline_exposure\":%g", d.color.dng_levels.baseline_exposure);
  // The size of the camera's own picture, smaller than the sensor's (Fuji).
  const libraw_raw_inset_crop_t &crop = d.sizes.raw_inset_crops[0];
  if (crop.cwidth && crop.cheight)
    printf(",\"frame\":{\"width\":%d,\"height\":%d}", crop.cwidth, crop.cheight);
  if (preview)
    printf(",\"preview\":{\"offset\":%lld,\"length\":%u,\"width\":%d,\"height\":%d}",
           (long long)preview->toffset, preview->tlength, preview->twidth,
           preview->theight);
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
