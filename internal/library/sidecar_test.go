// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"path/filepath"
	"testing"
	"time"
)

// Names of real exports, from immich-go's tests (AGPL-3.0).
func TestASidecarIsFoundByItsName(t *testing.T) {
	const own, shared, none = "own", "shared", ""
	for _, c := range []struct{ media, sidecar, want string }{
		{"PXL_20211013_220651983.jpg", "PXL_20211013_220651983.jpg.json", own},
		{"PXL_20211013_220651958.jpg", "PXL_20211013_220651983.jpg.json", none},
		{"PXL_20220405_090123740.PORTRAIT-modifié.jpg", "PXL_20220405_090123740.PORTRAIT.jpg.json", shared},
		{"PXL_20220405_090123741.PORTRAIT-modifié.jpg", "PXL_20220405_090123740.PORTRAIT.jpg.json", none},
		{"DSC_0100.JPG", "DSC_0100.JPG.json", own},
		{"DSC_0101(1).JPG", "DSC_0101.JPG(1).json", own},
		{"DSC_0102(1).JPG", "DSC_0102.JPG(2).json", none},
		{"DSC_0103.JPG", "DSC_0103.JPG(1).json", none},
		{"DSC_0104(1).JPG", "DSC_0104.JPG.json", none},
		{"IMG_2710(1).HEIC", "IMG_2710.HEIC(1).json", own},
		{"PXL_20231118_035751175.MP.jpg", "PXL_20231118_035751175.MP.jpg.json", own},
		{"PXL_20230809_203449253.LONG_EXPOSURE-02.ORIGINA.jpg", "PXL_20230809_203449253.LONG_EXPOSURE-02.ORIGIN.json", own},
		{"05yqt21kruxwwlhhgrwrdyb6chhwszi9bqmzu16w0 2.jpg", "05yqt21kruxwwlhhgrwrdyb6chhwszi9bqmzu16w0 2.jp.json", own},
		{"😀😃😄😁😆😅😂🤣🥲☺️😊😇🙂🙃😉😌😍🥰😘😗😙😚😋😛.jpg", "😀😃😄😁😆😅😂🤣🥲☺️😊😇🙂🙃😉😌😍🥰😘😗😙😚😋.json", own},
		{"Backyard_ceremony_wedding_photography_xxxxxxx_m(494).jpg", "Backyard_ceremony_wedding_photography_xxxxxxx_(494).json", own},
		{"Backyard_ceremony_wedding_photography_xxxxxxx_m(185).jpg", "Backyard_ceremony_wedding_photography_xxxxxxx_(494).json", none},
		{"original_1d4caa6f-16c6-4c3d-901b-9387de10e528_P.jpg", "original_1d4caa6f-16c6-4c3d-901b-9387de10e528_.json", own},
		{"original_1d4caa6f-16c6-4c3d-901b-9387de10e528_P(1).jpg", "original_1d4caa6f-16c6-4c3d-901b-9387de10e528_.json", own},
		{"PXL_20210102_221126856.MP~2.jpg", "PXL_20210102_221126856.MP~2.jpg.json", own},
		{"13039_327707840323_537645323_9470255_27214_n(1).jpg", "13039_327707840323_537645323_9470255_27214_n.j(1).json", own},
		{"20161105_170829.jpg", "20161105_170829.jpg.supplemental-metadata.json", own},
		{"Screenshot_20231027_123303_Facebook.jpg", "Screenshot_20231027_123303_Facebook.jpg.supple.json", own},
		{"Screenshot_20231027_123303_Facebook(1).jpg", "Screenshot_20231027_123303_Facebook.jpg.supple(1).json", own},
		{"MVIMG_20191230_232926.jpg", "MVIMG_20191230_232926.jpg.supplemental-metadat.json", own},
		{"Screenshot_20200301-161151.png", "Screenshot_20200301-161151.png.supplemental-me.json", own},
		{"MVIMG_20200207_134534~2.jpg", "MVIMG_20200207_134534~2.jpg.supplemental-metad.json", own},
		{"Scan35(1).jpg", "Scan35.jpg.supplemental-metadata(1).json", own},
		{"CLIP0001(10).AVI", "CLIP0001.AVI.supplemental-metadata(10).json", own},
		{"IMAG0061-edited.JPG", "IMAG0061.JPG.supplemental-metadata.json", shared},
		{"IMG-20230325-WA0122~2-edited.jpg", "IMG-20230325-WA0122~2.jpg.supplemental-metadat.json", shared},
		{"2234089303984509579-edited.jpg", "2234089303984509579.supplemental-metadata.json", shared},
		{"Screenshot_20231027_123303_Facebook-edited.jpg", "Screenshot_20231027_123303_Facebook.jpg.supple.json", shared},
		{"Screenshot_2024-08-31-00-53-48-647_com.snapchat.jpg", "Screenshot_2024-08-31-00-53-48-647_com.snapcha.json", own},
		{"IMG_4488.HEIC", "IMG_4488.HEIC.supplemental-metadata.json", own},
		{"IMG_4488.MP4", "IMG_4488.HEIC.supplemental-metadata.json", shared},
		{"IMG_4488(1).HEIC", "IMG_4488.HEIC.supplemental-metadata(1).json", own},
		{"IMG_4488(1).MP4", "IMG_4488.HEIC.supplemental-metadata(1).json", shared},
		{"IMG_4488(2).HEIC", "IMG_4488.HEIC.supplemental-metadata(2).json", own},
		{"IMG_4488(2).MP4", "IMG_4488.HEIC.supplemental-metadata(2).json", shared},
		{"IMG_4488(1).MP4", "IMG_4488.HEIC.supplemental-metadata(2).json", none},
		{"IMG_4488.MP4", "IMG_4488.MP4.supplemental-metadata.json", own},
		{"PXL_20220615_101533000(1).MP4", "PXL_20220615_101533000(1).jpg.supplemental-metadata.json", shared},
		{"X-bearbeitet.jpg", "X.jpg.supplemental-metadata.json", shared},
		{"X-edytowany.jpg", "X.jpg.supplemental-metadata.json", shared},
		{"X-editado.jpg", "X.jpg.supplemental-metadata.json", shared},
		{"X-bewerkt.jpg", "X.jpg.supplemental-metadata.json", shared},
		{"X-編集済み.jpg", "X.jpg.supplemental-metadata.json", shared},
		{"20030616.jpg", "20030616.json", own},
		{"IMG_2019-1.jpg", "IMG_2019.jpg.supplemental-metadata.json", none},
		{"IMG_12.jpg", "IMG_1.jpg.json", none},
	} {
		dir := t.TempDir()
		media := filepath.Join(dir, c.media)
		write(t, dir, c.sidecar, []byte(`{"photoTakenTime":{"timestamp":"1563120000"}}`))
		got := none
		if findSidecar(media) != "" {
			got = own
		} else if _, _, ok := sidecarInfo(media); ok {
			got = shared
		}
		if got != c.want {
			t.Errorf("%s with %s: %q, want %q", c.media, c.sidecar, got, c.want)
		}
	}
}

func TestAPhotoAndItsEditedCopyShareADate(t *testing.T) {
	dir := t.TempDir()
	year := "Takeout/Google Photos/Photos from 2019"
	write(t, dir, year+"/IMG_4471.jpg", []byte("original"))
	write(t, dir, year+"/IMG_4471-edited.jpg", []byte("edited"))
	write(t, dir, year+"/IMG_4471.jpg.supplemental-metadata.json", []byte(`{"photoTakenTime":{"timestamp":"1563120000"}}`))
	items, err := ReadTakeout(dir)
	if err != nil || len(items) != 2 {
		t.Fatalf("%d items, %v", len(items), err)
	}
	moved := 0
	for _, item := range items {
		if item.Capture.Source != FromSidecar {
			t.Errorf("%s: %s, want the sidecar's date", item.Path, item.Capture.Source)
		}
		if item.Sidecar != "" {
			moved++
		}
	}
	if moved != 1 {
		t.Errorf("the sidecar goes with %d photos, want 1", moved)
	}
}

func TestAScanOfBefore1970KeepsItsDate(t *testing.T) {
	dir := t.TempDir()
	for name, at := range map[string]string{"scan.jpg": "-473385600", "clock.jpg": "-2082844800", "zero.jpg": "0"} {
		write(t, dir, name+".json", []byte(`{"photoTakenTime":{"timestamp":"`+at+`"}}`))
	}
	if when, _, ok := sidecarInfo(filepath.Join(dir, "scan.jpg")); !ok || when != time.Date(1955, 1, 1, 0, 0, 0, 0, time.UTC) {
		t.Errorf("%v %v, want 1955", when, ok)
	}
	for _, name := range []string{"clock.jpg", "zero.jpg"} {
		if when, _, ok := sidecarInfo(filepath.Join(dir, name)); ok {
			t.Errorf("%s dated %v", name, when)
		}
	}
}
