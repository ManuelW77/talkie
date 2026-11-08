//! Android: AAudio direkt. Alle Funktionen werden zur Laufzeit aus libaaudio.so geladen,
//! damit die App auch auf Android 8.x startet (setUsage/setInputPreset gibt es erst ab 9).

use crate::core::dsp::{PullResampler, PushResampler, RATE};
use crate::core::mixer::Mixer;
use anyhow::{anyhow, bail, Result};
use parking_lot::Mutex;
use std::ffi::{c_void, CStr};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{mpsc, Arc};

type Builder = c_void;
type Stream = c_void;
type DataCb = unsafe extern "C" fn(*mut Stream, *mut c_void, *mut c_void, i32) -> i32;
type ErrorCb = unsafe extern "C" fn(*mut Stream, *mut c_void, i32);

const DIRECTION_OUTPUT: i32 = 0;
const DIRECTION_INPUT: i32 = 1;
const FORMAT_PCM_FLOAT: i32 = 2;
const SHARING_SHARED: i32 = 1;
/// Ohne Low-Latency-Modus: nur so laufen die Sprach-Effekte (Echo-Unterdrückung) des Systems.
const PERFORMANCE_NONE: i32 = 10;
const USAGE_VOICE_COMMUNICATION: i32 = 2;
const CONTENT_SPEECH: i32 = 1;
const PRESET_VOICE_COMMUNICATION: i32 = 7;
const CALLBACK_CONTINUE: i32 = 0;

struct Api {
    create_builder: unsafe extern "C" fn(*mut *mut Builder) -> i32,
    set_direction: unsafe extern "C" fn(*mut Builder, i32),
    set_format: unsafe extern "C" fn(*mut Builder, i32),
    set_channels: unsafe extern "C" fn(*mut Builder, i32),
    set_rate: unsafe extern "C" fn(*mut Builder, i32),
    set_sharing: unsafe extern "C" fn(*mut Builder, i32),
    set_performance: unsafe extern "C" fn(*mut Builder, i32),
    set_data_cb: unsafe extern "C" fn(*mut Builder, DataCb, *mut c_void),
    set_error_cb: unsafe extern "C" fn(*mut Builder, ErrorCb, *mut c_void),
    open: unsafe extern "C" fn(*mut Builder, *mut *mut Stream) -> i32,
    delete_builder: unsafe extern "C" fn(*mut Builder) -> i32,
    start: unsafe extern "C" fn(*mut Stream) -> i32,
    close: unsafe extern "C" fn(*mut Stream) -> i32,
    get_rate: unsafe extern "C" fn(*mut Stream) -> i32,
    get_channels: unsafe extern "C" fn(*mut Stream) -> i32,
    // ab Android 9 (API 28)
    set_usage: Option<unsafe extern "C" fn(*mut Builder, i32)>,
    set_content: Option<unsafe extern "C" fn(*mut Builder, i32)>,
    set_preset: Option<unsafe extern "C" fn(*mut Builder, i32)>,
}

fn load() -> Result<Api> {
    unsafe {
        let lib = libc::dlopen(c"libaaudio.so".as_ptr(), libc::RTLD_NOW);
        if lib.is_null() {
            bail!("libaaudio fehlt");
        }
        let sym = |name: &CStr| -> *mut c_void { libc::dlsym(lib, name.as_ptr()) };
        macro_rules! req {
            ($n:expr) => {{
                let p = sym($n);
                if p.is_null() {
                    bail!("AAudio-Funktion fehlt: {:?}", $n);
                }
                std::mem::transmute(p)
            }};
        }
        macro_rules! opt {
            ($n:expr) => {{
                let p = sym($n);
                (!p.is_null()).then(|| std::mem::transmute(p))
            }};
        }
        Ok(Api {
            create_builder: req!(c"AAudio_createStreamBuilder"),
            set_direction: req!(c"AAudioStreamBuilder_setDirection"),
            set_format: req!(c"AAudioStreamBuilder_setFormat"),
            set_channels: req!(c"AAudioStreamBuilder_setChannelCount"),
            set_rate: req!(c"AAudioStreamBuilder_setSampleRate"),
            set_sharing: req!(c"AAudioStreamBuilder_setSharingMode"),
            set_performance: req!(c"AAudioStreamBuilder_setPerformanceMode"),
            set_data_cb: req!(c"AAudioStreamBuilder_setDataCallback"),
            set_error_cb: req!(c"AAudioStreamBuilder_setErrorCallback"),
            open: req!(c"AAudioStreamBuilder_openStream"),
            delete_builder: req!(c"AAudioStreamBuilder_delete"),
            start: req!(c"AAudioStream_requestStart"),
            close: req!(c"AAudioStream_close"),
            get_rate: req!(c"AAudioStream_getSampleRate"),
            get_channels: req!(c"AAudioStream_getChannelCount"),
            set_usage: opt!(c"AAudioStreamBuilder_setUsage"),
            set_content: opt!(c"AAudioStreamBuilder_setContentType"),
            set_preset: opt!(c"AAudioStreamBuilder_setInputPreset"),
        })
    }
}

struct InputCtx {
    tx: mpsc::Sender<Vec<f32>>,
    channels: usize,
    rs: Option<PushResampler>,
    mono: Vec<f32>,
    failed: Arc<AtomicBool>,
}

struct OutputCtx {
    mixer: Arc<Mutex<Mixer>>,
    channels: usize,
    rs: Option<PullResampler>,
    failed: Arc<AtomicBool>,
}

unsafe extern "C" fn input_cb(_s: *mut Stream, user: *mut c_void, data: *mut c_void, frames: i32) -> i32 {
    let ctx = &mut *(user as *mut InputCtx);
    let Some(rs) = ctx.rs.as_mut() else { return CALLBACK_CONTINUE };
    let samples = std::slice::from_raw_parts(data as *const f32, frames as usize * ctx.channels);
    ctx.mono.clear();
    ctx.mono.extend(samples.chunks(ctx.channels).map(|f| f.iter().sum::<f32>() / ctx.channels as f32));
    let mut out = Vec::with_capacity(ctx.mono.len() * 2);
    rs.process(&ctx.mono, &mut out);
    let _ = ctx.tx.send(out);
    CALLBACK_CONTINUE
}

unsafe extern "C" fn output_cb(_s: *mut Stream, user: *mut c_void, data: *mut c_void, frames: i32) -> i32 {
    let ctx = &mut *(user as *mut OutputCtx);
    let out = std::slice::from_raw_parts_mut(data as *mut f32, frames as usize * ctx.channels);
    let Some(rs) = ctx.rs.as_mut() else {
        out.fill(0.0);
        return CALLBACK_CONTINUE;
    };
    let mut m = ctx.mixer.lock();
    for f in out.chunks_mut(ctx.channels) {
        f.fill(rs.next(|| m.next_sample()));
    }
    CALLBACK_CONTINUE
}

unsafe extern "C" fn input_err(_s: *mut Stream, user: *mut c_void, err: i32) {
    log::warn!("mikrofon (aaudio): fehler {err} – öffne neu");
    (*(user as *mut InputCtx)).failed.store(true, Ordering::Relaxed);
}

unsafe extern "C" fn output_err(_s: *mut Stream, user: *mut c_void, err: i32) {
    log::warn!("lautsprecher (aaudio): fehler {err} – öffne neu");
    (*(user as *mut OutputCtx)).failed.store(true, Ordering::Relaxed);
}

pub struct AndroidIo {
    api: Api,
    input: *mut Stream,
    output: *mut Stream,
    // Kontexte müssen leben, solange die Ströme laufen
    _in_ctx: Box<InputCtx>,
    _out_ctx: Box<OutputCtx>,
}

impl Drop for AndroidIo {
    fn drop(&mut self) {
        unsafe {
            if !self.input.is_null() {
                (self.api.close)(self.input);
            }
            if !self.output.is_null() {
                (self.api.close)(self.output);
            }
        }
    }
}

unsafe fn open_stream(api: &Api, input: bool, cb: DataCb, ecb: ErrorCb, user: *mut c_void) -> Result<*mut Stream> {
    let mut b: *mut Builder = std::ptr::null_mut();
    if (api.create_builder)(&mut b) != 0 || b.is_null() {
        bail!("AAudio-Builder");
    }
    (api.set_direction)(b, if input { DIRECTION_INPUT } else { DIRECTION_OUTPUT });
    (api.set_format)(b, FORMAT_PCM_FLOAT);
    (api.set_channels)(b, 1);
    (api.set_rate)(b, RATE as i32);
    (api.set_sharing)(b, SHARING_SHARED);
    (api.set_performance)(b, PERFORMANCE_NONE);
    if let Some(f) = api.set_usage {
        f(b, USAGE_VOICE_COMMUNICATION);
    }
    if let Some(f) = api.set_content {
        f(b, CONTENT_SPEECH);
    }
    if input {
        if let Some(f) = api.set_preset {
            f(b, PRESET_VOICE_COMMUNICATION);
        }
    }
    (api.set_data_cb)(b, cb, user);
    (api.set_error_cb)(b, ecb, user);
    let mut s: *mut Stream = std::ptr::null_mut();
    let r = (api.open)(b, &mut s);
    (api.delete_builder)(b);
    if r != 0 || s.is_null() {
        return Err(anyhow!("AAudio öffnen: {r}"));
    }
    Ok(s)
}

pub fn open(tx: mpsc::Sender<Vec<f32>>, mixer: Arc<Mutex<Mixer>>, failed: Arc<AtomicBool>) -> Result<AndroidIo> {
    let api = load()?;
    let mut in_ctx = Box::new(InputCtx { tx, channels: 1, rs: None, mono: Vec::with_capacity(4096), failed: failed.clone() });
    let mut out_ctx = Box::new(OutputCtx { mixer, channels: 1, rs: None, failed });
    unsafe {
        let input = open_stream(&api, true, input_cb, input_err, &mut *in_ctx as *mut InputCtx as *mut c_void)?;
        let output = match open_stream(&api, false, output_cb, output_err, &mut *out_ctx as *mut OutputCtx as *mut c_void) {
            Ok(o) => o,
            Err(e) => {
                (api.close)(input);
                return Err(e);
            }
        };
        // Tatsächliches Format übernehmen, bevor die Callbacks loslegen
        in_ctx.channels = (api.get_channels)(input).max(1) as usize;
        in_ctx.rs = Some(PushResampler::new((api.get_rate)(input).max(8000) as u32, RATE));
        out_ctx.channels = (api.get_channels)(output).max(1) as usize;
        out_ctx.rs = Some(PullResampler::new(RATE, (api.get_rate)(output).max(8000) as u32));
        let io = AndroidIo { api, input, output, _in_ctx: in_ctx, _out_ctx: out_ctx };
        if (io.api.start)(io.input) != 0 || (io.api.start)(io.output) != 0 {
            bail!("AAudio starten");
        }
        log::info!("aaudio mit sprachverarbeitung gestartet");
        Ok(io)
    }
}
