use std::sync::Mutex;
use std::time::Duration;

use tauri::{Emitter, Manager, RunEvent};
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;
use tokio::sync::watch;

const READY_PREFIX: &str = "NIMBO_LISTENING ";
const READY_TIMEOUT: Duration = Duration::from_secs(30);

/// The nimbo sidecar: its base URL once it prints `NIMBO_LISTENING <url>`
/// (or the reason it never will), and the child process to kill on exit.
struct Engine {
  ready: watch::Receiver<Option<Result<String, String>>>,
  child: Mutex<Option<CommandChild>>,
}

#[tauri::command]
async fn engine_url(state: tauri::State<'_, Engine>) -> Result<String, String> {
  let mut ready = state.ready.clone();
  let wait = async move {
    loop {
      let current = ready.borrow_and_update().clone();
      if let Some(result) = current {
        return result;
      }
      if ready.changed().await.is_err() {
        return Err("the nimbo engine stopped before it was ready".to_string());
      }
    }
  };
  tokio::time::timeout(READY_TIMEOUT, wait)
    .await
    .map_err(|_| format!("the nimbo engine did not start within {}s", READY_TIMEOUT.as_secs()))?
}

fn spawn_engine(app: &tauri::AppHandle, tx: watch::Sender<Option<Result<String, String>>>) -> Option<CommandChild> {
  let spawned = app
    .shell()
    .sidecar("nimbo")
    .map(|cmd| cmd.args(["serve", "--addr", "127.0.0.1:0", "--exit-with-parent"]))
    .and_then(|cmd| cmd.spawn());
  let (mut events, child) = match spawned {
    Ok(pair) => pair,
    Err(err) => {
      tx.send_replace(Some(Err(format!("could not start the nimbo engine: {err}"))));
      return None;
    }
  };

  tauri::async_runtime::spawn(async move {
    while let Some(event) = events.recv().await {
      match event {
        CommandEvent::Stdout(bytes) => {
          for line in String::from_utf8_lossy(&bytes).lines() {
            match line.trim().strip_prefix(READY_PREFIX) {
              Some(url) => {
                tx.send_replace(Some(Ok(url.trim().to_string())));
              }
              None => log::info!("nimbo: {line}"),
            }
          }
        }
        CommandEvent::Stderr(bytes) => {
          for line in String::from_utf8_lossy(&bytes).lines() {
            log::info!("nimbo: {line}");
          }
        }
        CommandEvent::Error(err) => log::error!("nimbo: {err}"),
        CommandEvent::Terminated(status) => {
          let message = format!("the nimbo engine exited (code {:?})", status.code);
          log::error!("{message}");
          tx.send_if_modified(|state| {
            if state.as_ref().is_some_and(|r| r.is_ok()) {
              return false;
            }
            *state = Some(Err(message.clone()));
            true
          });
          break;
        }
        _ => {}
      }
    }
  });
  Some(child)
}

/// Menu item ids; the webview listens for [`CLOSE_WORKFLOW`] events.
const CLOSE_WORKFLOW: &str = "close-workflow";
const CLOSE_WINDOW: &str = "close-window";

/// The default macOS menu, except ⌘W closes the open workflow (emitted to the
/// webview) instead of the window; closing the window moves to ⇧⌘W.
#[cfg(target_os = "macos")]
fn app_menu(app: &tauri::AppHandle) -> tauri::Result<tauri::menu::Menu<tauri::Wry>> {
  use tauri::menu::{AboutMetadata, Menu, MenuItem, PredefinedMenuItem, Submenu, HELP_SUBMENU_ID, WINDOW_SUBMENU_ID};
  let pkg = app.package_info();
  let about = AboutMetadata {
    name: Some(pkg.name.clone()),
    version: Some(pkg.version.to_string()),
    copyright: app.config().bundle.copyright.clone(),
    authors: app.config().bundle.publisher.clone().map(|p| vec![p]),
    ..Default::default()
  };
  Menu::with_items(
    app,
    &[
      &Submenu::with_items(
        app,
        pkg.name.clone(),
        true,
        &[
          &PredefinedMenuItem::about(app, None, Some(about))?,
          &PredefinedMenuItem::separator(app)?,
          &PredefinedMenuItem::services(app, None)?,
          &PredefinedMenuItem::separator(app)?,
          &PredefinedMenuItem::hide(app, None)?,
          &PredefinedMenuItem::hide_others(app, None)?,
          &PredefinedMenuItem::separator(app)?,
          &PredefinedMenuItem::quit(app, None)?,
        ],
      )?,
      &Submenu::with_items(
        app,
        "File",
        true,
        &[&MenuItem::with_id(app, CLOSE_WORKFLOW, "Close Workflow", true, Some("CmdOrCtrl+W"))?],
      )?,
      &Submenu::with_items(
        app,
        "Edit",
        true,
        &[
          &PredefinedMenuItem::undo(app, None)?,
          &PredefinedMenuItem::redo(app, None)?,
          &PredefinedMenuItem::separator(app)?,
          &PredefinedMenuItem::cut(app, None)?,
          &PredefinedMenuItem::copy(app, None)?,
          &PredefinedMenuItem::paste(app, None)?,
          &PredefinedMenuItem::select_all(app, None)?,
        ],
      )?,
      &Submenu::with_items(app, "View", true, &[&PredefinedMenuItem::fullscreen(app, None)?])?,
      &Submenu::with_id_and_items(
        app,
        WINDOW_SUBMENU_ID,
        "Window",
        true,
        &[
          &PredefinedMenuItem::minimize(app, None)?,
          &PredefinedMenuItem::maximize(app, None)?,
          &PredefinedMenuItem::separator(app)?,
          &MenuItem::with_id(app, CLOSE_WINDOW, "Close Window", true, Some("CmdOrCtrl+Shift+W"))?,
        ],
      )?,
      &Submenu::with_id_and_items(app, HELP_SUBMENU_ID, "Help", true, &[])?,
    ],
  )
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
  let builder = tauri::Builder::default();
  #[cfg(target_os = "macos")]
  let builder = builder.menu(app_menu);
  let app = builder
    .plugin(tauri_plugin_shell::init())
    .plugin(tauri_plugin_dialog::init())
    .plugin(tauri_plugin_fs::init())
    .invoke_handler(tauri::generate_handler![engine_url])
    .on_menu_event(|app, event| {
      if event.id() == CLOSE_WORKFLOW {
        let _ = app.emit("close-workflow", ());
      } else if event.id() == CLOSE_WINDOW
        && let Some(window) = app.get_webview_window("main")
      {
        let _ = window.close();
      }
    })
    .setup(|app| {
      if cfg!(debug_assertions) {
        app.handle().plugin(
          tauri_plugin_log::Builder::default()
            .level(log::LevelFilter::Info)
            .build(),
        )?;
      }
      let (tx, rx) = watch::channel(None);
      let child = spawn_engine(app.handle(), tx);
      app.manage(Engine {
        ready: rx,
        child: Mutex::new(child),
      });
      Ok(())
    })
    .build(tauri::generate_context!())
    .expect("error while building tauri application");

  app.run(|app, event| {
    if let RunEvent::Exit = event {
      let child = app.state::<Engine>().child.lock().ok().and_then(|mut c| c.take());
      if let Some(child) = child {
        let _ = child.kill();
      }
    }
  });
}
