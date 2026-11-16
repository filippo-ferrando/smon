// Package gst runs GStreamer pipelines in process and picks the H.264
// encoder.
package gst

/*
#cgo pkg-config: gstreamer-1.0 gstreamer-video-1.0
#include <stdlib.h>
#include <gst/gst.h>
#include <gst/video/video.h>

static void smon_gst_init(void) { gst_init(NULL, NULL); }

static GstElement *smon_launch(const char *desc, char **err) {
	GError *e = NULL;
	GstElement *p = gst_parse_launch(desc, &e);
	if (e != NULL) {
		*err = g_strdup(e->message);
		g_error_free(e);
		if (p != NULL) gst_object_unref(p);
		return NULL;
	}
	if (gst_element_set_state(p, GST_STATE_PLAYING) == GST_STATE_CHANGE_FAILURE) {
		*err = g_strdup("pipeline failed to start");
		gst_element_set_state(p, GST_STATE_NULL);
		gst_object_unref(p);
		return NULL;
	}
	return p;
}

// smon_wait blocks until the pipeline errors, ends, or is asked to stop, then
// tears it down. It returns NULL or the error message (caller frees).
static char *smon_wait(GstElement *p) {
	GstBus *bus = gst_element_get_bus(p);
	GstMessage *m = gst_bus_timed_pop_filtered(bus, GST_CLOCK_TIME_NONE,
		GST_MESSAGE_ERROR | GST_MESSAGE_EOS | GST_MESSAGE_APPLICATION);
	char *err = NULL;
	if (GST_MESSAGE_TYPE(m) == GST_MESSAGE_ERROR) {
		GError *e = NULL;
		gst_message_parse_error(m, &e, NULL);
		err = g_strdup_printf("%s: %s", GST_OBJECT_NAME(GST_MESSAGE_SRC(m)), e->message);
		g_error_free(e);
	} else if (GST_MESSAGE_TYPE(m) == GST_MESSAGE_EOS) {
		err = g_strdup("end of stream");
	}
	gst_message_unref(m);
	gst_object_unref(bus);
	gst_element_set_state(p, GST_STATE_NULL);
	return err;
}

static void smon_stop(GstElement *p) {
	GstBus *bus = gst_element_get_bus(p);
	gst_bus_post(bus, gst_message_new_application(GST_OBJECT(p), gst_structure_new_empty("smon-stop")));
	gst_object_unref(bus);
}

// smon_force_key asks element name for an IDR (with SPS/PPS) as soon as possible.
static int smon_force_key(GstElement *p, const char *name) {
	GstElement *el = gst_bin_get_by_name(GST_BIN(p), name);
	if (el == NULL) return 0;
	GstPad *src = gst_element_get_static_pad(el, "src");
	int ok = 0;
	if (src != NULL) {
		ok = gst_pad_send_event(src, gst_video_event_new_upstream_force_key_unit(GST_CLOCK_TIME_NONE, TRUE, 0));
		gst_object_unref(src);
	}
	gst_object_unref(el);
	return ok;
}

static int smon_set_uint(GstElement *p, const char *name, const char *prop, guint v) {
	GstElement *el = gst_bin_get_by_name(GST_BIN(p), name);
	if (el == NULL) return 0;
	int ok = g_object_class_find_property(G_OBJECT_GET_CLASS(el), prop) != NULL;
	if (ok) g_object_set(el, prop, v, NULL);
	gst_object_unref(el);
	return ok;
}
*/
import "C"

import (
	"errors"
	"sync"
	"time"
	"unsafe"
)

var gstInit sync.Once

// Pipeline is a GStreamer pipeline running in this process. Unlike a
// gst-launch child it can be steered while it plays: forced keyframes when a
// viewer needs one, and live bitrate changes. The encoder must be named "enc".
type Pipeline struct {
	mu   sync.Mutex
	p    *C.GstElement // nil once the pipeline has ended
	done chan struct{}
	err  error

	keyMu      sync.Mutex
	lastKey    time.Time
	keyPending bool
}

// keyInterval bounds forced keyframes: each is several times a normal frame,
// and a lossy link can ask for one per lost packet.
const keyInterval = 500 * time.Millisecond

// Start parses desc and sets the pipeline playing.
func Start(desc string) (*Pipeline, error) {
	gstInit.Do(func() { C.smon_gst_init() })
	cdesc := C.CString(desc)
	defer C.free(unsafe.Pointer(cdesc))
	var cerr *C.char
	p := C.smon_launch(cdesc, &cerr)
	if p == nil {
		defer C.free(unsafe.Pointer(cerr))
		return nil, errors.New(C.GoString(cerr))
	}
	g := &Pipeline{p: p, done: make(chan struct{})}
	go func() {
		cerr := C.smon_wait(p)
		g.mu.Lock()
		g.p = nil
		if cerr != nil {
			g.err = errors.New(C.GoString(cerr))
			C.free(unsafe.Pointer(cerr))
		}
		g.mu.Unlock()
		C.gst_object_unref(C.gpointer(p))
		close(g.done)
	}()
	return g, nil
}

// Wait blocks until the pipeline has ended and returns why; nil after Stop.
func (g *Pipeline) Wait() error {
	<-g.done
	return g.err
}

// Stop ends the pipeline and waits for it to shut down. Safe to call twice.
func (g *Pipeline) Stop() {
	g.mu.Lock()
	if g.p != nil {
		C.smon_stop(g.p)
	}
	g.mu.Unlock()
	<-g.done
}

// Keyframe asks the encoder for an IDR. Requests within keyInterval of the
// last keyframe are merged into one sent when the interval ends.
func (g *Pipeline) Keyframe() {
	g.keyMu.Lock()
	defer g.keyMu.Unlock()
	if g.keyPending {
		return
	}
	if wait := keyInterval - time.Since(g.lastKey); wait > 0 {
		g.keyPending = true
		time.AfterFunc(wait, func() {
			g.keyMu.Lock()
			g.keyPending = false
			g.lastKey = time.Now()
			g.keyMu.Unlock()
			g.forceKey()
		})
		return
	}
	g.lastKey = time.Now()
	g.forceKey()
}

func (g *Pipeline) forceKey() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.p != nil {
		name := C.CString("enc")
		defer C.free(unsafe.Pointer(name))
		C.smon_force_key(g.p, name)
	}
}

// SetBitrate changes the encoder's target bitrate while it plays. For VA-API
// encoders the rate-control buffer follows, so it stays ~200 ms long.
func (g *Pipeline) SetBitrate(kbps int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.p == nil {
		return
	}
	set := func(prop string, v int) {
		name, cprop := C.CString("enc"), C.CString(prop)
		defer C.free(unsafe.Pointer(name))
		defer C.free(unsafe.Pointer(cprop))
		C.smon_set_uint(g.p, name, cprop, C.guint(v))
	}
	set("bitrate", kbps)
	set("cpb-size", cpbSize(kbps)) // VA-API only; x264's VBV is in ms already
}
