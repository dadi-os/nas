package browser

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// uploadTriggerTimeout is how long a clicked control may take to open a file chooser or
	// add a file input.
	uploadTriggerTimeout = 10 * time.Second
	// uploadPoll is how often the page is searched for a file input a click added.
	uploadPoll = 250 * time.Millisecond
)

type fileInfo struct {
	Multiple bool `json:"multiple"`
	Files    []struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	} `json:"files"`
}

// Upload attaches the host files at paths to a file input. A ref on a file input (in the
// page, a frame or a shadow root) receives them directly; any other ref is clicked, and the
// files go to the file chooser the click opens or, when it opens an upload dialog instead,
// to the one file input that appears in any frame. It returns the names the input holds.
func (t *Tab) Upload(ctx context.Context, ref string, paths []string) (string, error) {
	if len(paths) == 0 {
		return "", fmt.Errorf("upload needs at least one path")
	}
	for _, p := range paths {
		info, err := os.Stat(p)
		switch {
		case !filepath.IsAbs(p):
			return "", fmt.Errorf("%s is not an absolute path", p)
		case err != nil:
			return "", fmt.Errorf("no file at %s on the host", p)
		case info.IsDir():
			return "", fmt.Errorf("%s is a directory", p)
		}
	}
	el, err := t.resolve(ctx, ref)
	if err != nil {
		return "", err
	}
	var info *fileInfo
	if err := el.call(ctx, fileInfoFn, &info); err != nil {
		return "", err
	}
	target := el
	if info == nil {
		if target, err = t.inputFromClick(ctx, ref); err != nil {
			return "", err
		}
		if err := target.call(ctx, fileInfoFn, &info); err != nil {
			return "", err
		}
	}
	if len(paths) > 1 && !info.Multiple {
		return "", fmt.Errorf("the file input behind %s accepts one file; %d were given", ref, len(paths))
	}
	if err := t.b.c.send(ctx, target.f.sess, "DOM.setFileInputFiles", map[string]any{"files": paths, "objectId": target.oid}, nil); err != nil {
		return "", err
	}
	if err := target.call(ctx, fileInfoFn, &info); err != nil {
		return "", err
	}
	var names []string
	for _, f := range info.Files {
		names = append(names, f.Name)
		if f.Size == 0 {
			return "", fmt.Errorf("upload did not attach as given: %s is empty on the input", f.Name)
		}
	}
	want := make([]string, len(paths))
	for i, p := range paths {
		want[i] = filepath.Base(p)
	}
	if strings.Join(names, "\x00") != strings.Join(want, "\x00") {
		return "", fmt.Errorf("upload did not attach as given: expected %v, the input holds %v", want, names)
	}
	return strings.Join(names, ", "), nil
}

// inputFromClick clicks the element at ref and returns the file input it hands files to: the
// one behind the file chooser the click opens, or the one new file input that appears in any
// frame, as an upload dialog adds it.
func (t *Tab) inputFromClick(ctx context.Context, ref string) (element, error) {
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		return element{}, err
	}
	seen := hex.EncodeToString(raw)
	frames, err := t.framesInOrder(ctx)
	if err != nil {
		return element{}, err
	}
	for _, f := range frames {
		if err := t.eval(ctx, f, fmt.Sprintf("(%s)(%s)", markFileInputsScript, jsonString(seen)), nil); err != nil {
			return element{}, err
		}
	}
	select {
	case <-t.chooser:
	default:
	}
	if err := t.b.c.send(ctx, t.sid, "Page.setInterceptFileChooserDialog", map[string]any{"enabled": true}, nil); err != nil {
		return element{}, err
	}
	defer func() {
		off, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		_ = t.b.c.send(off, t.sid, "Page.setInterceptFileChooserDialog", map[string]any{"enabled": false}, nil)
	}()
	if err := t.Click(ctx, ref, false); err != nil {
		return element{}, err
	}
	deadline := time.Now().Add(uploadTriggerTimeout)
	for {
		select {
		case opened := <-t.chooser:
			f, ok := t.frameByID(opened.frameID)
			if !ok {
				return element{}, fmt.Errorf("the file chooser opened in a frame that is gone")
			}
			var node struct {
				Object struct {
					ObjectID string `json:"objectId"`
				} `json:"object"`
			}
			if err := t.b.c.send(ctx, f.sess, "DOM.resolveNode", map[string]any{"backendNodeId": opened.node}, &node); err != nil {
				return element{}, err
			}
			return element{t: t, f: f, oid: node.Object.ObjectID}, nil
		default:
		}
		current, err := t.framesInOrder(ctx)
		if err != nil {
			return element{}, err
		}
		var found []element
		for _, f := range current {
			oid, val, err := t.evalObject(ctx, f, fmt.Sprintf("(%s)(%s)", newFileInputsScript, jsonString(seen)))
			if err != nil {
				continue
			}
			if oid != "" {
				found = append(found, element{t: t, f: f, oid: oid})
			} else if string(val) != "0" && len(val) > 0 {
				return element{}, fmt.Errorf("the click added %s file inputs; upload to one by its ref", val)
			}
		}
		if len(found) > 1 {
			return element{}, fmt.Errorf("the click added %d file inputs; upload to one by its ref", len(found))
		}
		if len(found) == 1 {
			return found[0], nil
		}
		if time.Now().After(deadline) {
			return element{}, fmt.Errorf("clicking %s opened no file chooser and added no file input within %s; look again and pick the control that opens the upload", ref, uploadTriggerTimeout)
		}
		select {
		case <-time.After(uploadPoll):
		case <-ctx.Done():
			return element{}, ctx.Err()
		}
	}
}
