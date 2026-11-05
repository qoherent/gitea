// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"strings"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/modules/httplib"
	"gitea.dev/modules/log"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/storage"
	"gitea.dev/services/context"
)

// IsArtifactV4 detects whether the artifact is likely from v4.
// V4 backend stores the files as a single combined zip file per artifact, and ensures ContentEncoding contains a slash
// (otherwise this uses application/zip instead of the custom mime type), which is not the case for the old backend.
func IsArtifactV4(art *actions_model.ActionArtifact) bool {
	return strings.Contains(art.ContentEncodingOrType, "/")
}

func GetArtifactV4ServeDirectURL(art *actions_model.ActionArtifact, method string) (string, error) {
	contentType := art.ContentEncodingOrType
	u, err := storage.ActionsArtifacts.ServeDirectURL(art.StoragePath, art.ArtifactPath, method, &storage.ServeDirectOptions{ContentType: contentType})
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func DownloadArtifactV4ServeDirect(ctx *context.Base, art *actions_model.ActionArtifact) bool {
	if !setting.Actions.ArtifactStorage.ServeDirect() {
		return false
	}
	u, err := GetArtifactV4ServeDirectURL(art, ctx.Req.Method)
	if err != nil {
		log.Error("GetArtifactV4ServeDirectURL: %v", err)
		return false
	}
	ctx.Redirect(u, http.StatusFound)
	return true
}

func DownloadArtifactV4ReadStorage(ctx *context.Base, art *actions_model.ActionArtifact) error {
	f, err := storage.ActionsArtifacts.Open(art.StoragePath)
	if err != nil {
		return err
	}
	defer f.Close()
	httplib.ServeUserContentByFile(ctx.Req, ctx.Resp, f, httplib.ServeHeaderOptions{
		Filename:    art.ArtifactPath,
		ContentType: art.ContentEncodingOrType, // v4 guarantees that the field is Content-Type
	})
	return nil
}

func DownloadArtifactV4(ctx *context.Base, art *actions_model.ActionArtifact) error {
	if DownloadArtifactV4ServeDirect(ctx, art) {
		return nil
	}
	return DownloadArtifactV4ReadStorage(ctx, art)
}

// DownloadArtifactV3 writes a v1-v3 artifact to the response as a zip.
//
// The old backend stores one file per row, so an artifact is the group of
// rows sharing a run, an attempt and a name, and downloading one means
// zipping them. Shared by the web view and the API rather than written
// twice: the API refused these outright, so a client could see an artifact
// listed and never fetch it.
func DownloadArtifactV3(w http.ResponseWriter, artifactName string, artifacts []*actions_model.ActionArtifact) error {
	w.Header().Set("Content-Disposition", httplib.EncodeContentDispositionAttachment(artifactName+".zip"))
	zipWriter := zip.NewWriter(w)
	defer zipWriter.Close()

	for _, art := range artifacts {
		if err := writeArtifactToZip(zipWriter, art); err != nil {
			return err
		}
	}
	return nil
}

func writeArtifactToZip(zipWriter *zip.Writer, art *actions_model.ActionArtifact) error {
	f, err := storage.ActionsArtifacts.Open(art.StoragePath)
	if err != nil {
		return fmt.Errorf("ActionsArtifacts.Open: %w", err)
	}
	defer f.Close()

	var r io.ReadCloser = f
	if art.ContentEncodingOrType == actions_model.ContentEncodingV3Gzip {
		r, err = gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("gzip.NewReader: %w", err)
		}
	}
	defer r.Close()

	dest, err := zipWriter.Create(art.ArtifactPath)
	if err != nil {
		return fmt.Errorf("zipWriter.Create: %w", err)
	}
	if _, err = io.Copy(dest, r); err != nil {
		return fmt.Errorf("io.Copy: %w", err)
	}
	return nil
}
