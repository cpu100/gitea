// Copyright 2021 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package common

import (
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"code.gitea.io/gitea/modules/charset"
	"code.gitea.io/gitea/modules/context"
	"code.gitea.io/gitea/modules/git"
	"code.gitea.io/gitea/modules/httpcache"
	"code.gitea.io/gitea/modules/log"
	"code.gitea.io/gitea/modules/setting"
	"code.gitea.io/gitea/modules/typesniffer"
	"code.gitea.io/gitea/modules/util"
)

// ServeBlob download a git.Blob
func ServeBlob(ctx *context.Context, blob *git.Blob) error {
	if httpcache.HandleGenericETagCache(ctx.Req, ctx.Resp, `"`+blob.ID.String()+`"`) {
		return nil
	}

	dataRc, err := blob.DataAsync()
	if err != nil {
		return err
	}
	defer func() {
		if err = dataRc.Close(); err != nil {
			log.Error("ServeBlob: Close: %v", err)
		}
	}()

	return ServeData(ctx, ctx.Repo.TreePath, blob.Size(), dataRc)
}

// ServeData download file from io.Reader
func ServeData(ctx *context.Context, name string, size int64, reader io.Reader) error {

	// Chrome Dev / 网络 / 节流模式
	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Range
	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Range_requests
	if _, ok := reader.(io.ReaderAt); ok {
		if rng := ctx.Req.Header.Get("Range"); len(rng) > 0 {
			var start int
			var end int
			var err, err2 error
			// Range: bytes=131072-
			arr := strings.Split(strings.TrimLeft(rng, "bytes="), "-")
			start, err = strconv.Atoi(arr[0])
			if len(arr[1]) == 0 {
				end = int(size - 1)
			} else {
				end, err2 = strconv.Atoi(arr[1])
				if int64(end) > size-1 {
					end = int(size - 1)
				}
			}

			length := end - start + 1
			if length <= 0 || nil != err || nil != err2 {
				return fmt.Errorf("invalid range header: %s", rng)
			}

			log.Warn("%s start:%d end:%d len:%d", rng, start, end, length)

			//ctx.Status(206)
			//ctx.Resp.Header().Set("Content-Length", strconv.Itoa(length))
			//ctx.Resp.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
			//// todo use bytes.Reader
			//if start > 0 {
			//	num := start + 1
			//	buf := make([]byte, 1024)
			//	for i := 0; ; {
			//		i += 1024
			//		if i > num {
			//			if _, err := reader.Read(buf[:num%1024]); err != nil {
			//				return err
			//			}
			//			break
			//		}
			//		if _, err := reader.Read(buf); err != nil {
			//			return err
			//		}
			//	}
			//}
			//_, err = io.CopyN(ctx.Resp, reader, int64(length))
			//return err
		} else {
			ctx.Resp.Header().Set("Accept-Ranges", "bytes")
		}
	}

	buf := make([]byte, 1024)
	n, err := util.ReadAtMost(reader, buf)
	if err != nil {
		return err
	}
	if n >= 0 {
		buf = buf[:n]
	}

	ctx.Resp.Header().Set("Cache-Control", "public,max-age=86400")

	if size >= 0 {
		ctx.Resp.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	} else {
		log.Error("ServeData called to serve data: %s with size < 0: %d", name, size)
	}
	name = path.Base(name)

	// Google Chrome dislike commas in filenames, so let's change it to a space
	name = strings.ReplaceAll(name, ",", " ")

	st := typesniffer.DetectContentType(buf)

	mappedMimeType := ""
	if setting.MimeTypeMap.Enabled {
		fileExtension := strings.ToLower(filepath.Ext(name))
		mappedMimeType = setting.MimeTypeMap.Map[fileExtension]
	}
	if st.IsText() || ctx.FormBool("render") {
		cs, err := charset.DetectEncoding(buf)
		if err != nil {
			log.Error("Detect raw file %s charset failed: %v, using by default utf-8", name, err)
			cs = "utf-8"
		}
		if mappedMimeType == "" {
			mappedMimeType = "text/plain"
		}
		ctx.Resp.Header().Set("Content-Type", mappedMimeType+"; charset="+strings.ToLower(cs))
	} else {
		ctx.Resp.Header().Set("Access-Control-Expose-Headers", "Content-Disposition")
		if mappedMimeType != "" {
			ctx.Resp.Header().Set("Content-Type", mappedMimeType)
		}
		if (st.IsImage() || st.IsPDF()) && (setting.UI.SVG.Enabled || !st.IsSvgImage()) {
			ctx.Resp.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, name))
			if st.IsSvgImage() {
				ctx.Resp.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
				ctx.Resp.Header().Set("X-Content-Type-Options", "nosniff")
				ctx.Resp.Header().Set("Content-Type", typesniffer.SvgMimeType)
			}
		} else {
			ctx.Resp.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
		}
	}

	_, err = ctx.Resp.Write(buf)
	if err != nil {
		return err
	}
	_, err = io.Copy(ctx.Resp, reader)
	return err
}
