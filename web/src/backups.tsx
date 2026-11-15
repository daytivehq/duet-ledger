import { useEffect, useState, type FormEvent } from "react";
import { api, post, download } from "./api";
import { msg, intlLocale } from "./i18n";
import { Modal, Icon, ErrorText, Busy, PasswordInput } from "./ui";
import { useTask } from "./use-task";
import type { DialogProps } from "./dialogs";

type Backup = {
  name: string;
  size: number;
  created_at: string;
  before_restore?: boolean;
  encrypted?: boolean;
};
type Preview = {
  token: string;
  created_at: string;
  users: number;
  accounts: number;
  transactions: number;
  receipts: number;
  expires_at: string;
};

function AuthenticationFields(p: {
  password: string;
  setPassword: (value: string) => void;
  code: string;
  setCode: (value: string) => void;
  totp: boolean;
  idPrefix?: string;
}) {
  return (
    <>
      <label
        className="form-label"
        htmlFor={`${p.idPrefix || "backup"}-password`}
      >
        {msg("현재 비밀번호")}
      </label>
      <PasswordInput
        id={`${p.idPrefix || "backup"}-password`}
        value={p.password}
        onChange={(e) => p.setPassword(e.target.value)}
        autoComplete="current-password"
        required
      />
      {p.totp && (
        <>
          <label
            className="form-label"
            htmlFor={`${p.idPrefix || "backup"}-code`}
          >
            {msg("인증 코드 또는 복구 코드")}
          </label>
          <input
            id={`${p.idPrefix || "backup"}-code`}
            className="field-input"
            value={p.code}
            onChange={(e) => p.setCode(e.target.value)}
            autoComplete="one-time-code"
            required
          />
        </>
      )}
    </>
  );
}

export function BackupDialog(p: DialogProps) {
  const [tab, setTab] = useState<"backup" | "restore">("backup");
  const [backups, setBackups] = useState<Backup[]>([]);
  const [source, setSource] = useState("");
  const [archive, setArchive] = useState<File>();
  const [key, setKey] = useState<File>();
  const [preview, setPreview] = useState<Preview>();
  const [keyForm, setKeyForm] = useState(false);
  const [configured, setConfigured] = useState(false);
  const [settingsForm, setSettingsForm] = useState(false);
  const [backupPassword, setBackupPassword] = useState("");
  const [backupPasswordConfirm, setBackupPasswordConfirm] = useState("");
  const [restorePassword, setRestorePassword] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const task = useTask();
  async function load() {
    const [items, settings] = await Promise.all([
      api<Backup[]>("/backups"),
      api<{ configured: boolean }>("/backups/settings"),
    ]);
    setBackups(items);
    setConfigured(settings.configured);
  }
  useEffect(() => {
    load().catch((e) => task.setError(e.message));
  }, []);
  const auth = {
    password,
    setPassword,
    code,
    setCode,
    totp: p.user.totp_enabled,
  };
  function resetPreview() {
    setPreview(undefined);
    setConfirmed(false);
    setPassword("");
    setCode("");
    task.setError("");
  }
  async function inspect(e: FormEvent) {
    e.preventDefault();
    await task.run(async () => {
      const form = new FormData();
      if (source) form.append("name", source);
      else if (archive) form.append("archive", archive);
      else throw new Error(msg("복원할 백업을 하나 선택해 주세요."));
      if (key) form.append("key", key);
      if (restorePassword) form.append("backup_password", restorePassword);
      setPreview(
        await api<Preview>("/backups/restore/preview", {
          method: "POST",
          body: form,
        }),
      );
      setPassword("");
      setCode("");
      setRestorePassword("");
      setConfirmed(false);
    });
  }
  async function restore(e: FormEvent) {
    e.preventDefault();
    if (!preview || !confirmed) return;
    await task.run(async () => {
      await post("/backups/restore/confirm", {
        token: preview.token,
        password,
        code,
        confirm: true,
      });
      window.location.reload();
    });
  }
  async function saveBackupPassword(e: FormEvent) {
    e.preventDefault();
    await task.run(async () => {
      if (backupPassword !== backupPasswordConfirm)
        throw new Error(msg("백업 비밀번호가 일치하지 않아요."));
      await post("/backups/settings", {
        password,
        code,
        backup_password: backupPassword,
      });
      setConfigured(true);
      setSettingsForm(false);
      setBackupPassword("");
      setBackupPasswordConfirm("");
      setPassword("");
      setCode("");
      p.notify(
        msg(
          "백업 비밀번호를 설정했어요. 기존 백업에는 이전 비밀번호가 필요해요.",
        ),
      );
    });
  }
  async function saveKey(e: FormEvent) {
    e.preventDefault();
    await task.run(async () => {
      const result = await post<{ key: string }>("/backups/key", {
        password,
        code,
      });
      const bytes = Uint8Array.from(atob(result.key), (c) => c.charCodeAt(0));
      const url = URL.createObjectURL(
        new Blob([bytes], { type: "application/octet-stream" }),
      );
      const link = document.createElement("a");
      link.href = url;
      link.download = "master.key";
      link.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
      setPassword("");
      setCode("");
      setKeyForm(false);
      p.notify(msg("키 파일을 백업 ZIP과 함께 안전하게 보관해 주세요."));
    });
  }
  return (
    <Modal
      title={msg("백업과 복원")}
      subtitle={msg("서버 관리자 전용 · 모든 사용자의 자료")}
      onClose={() => {
        if (!task.busy) p.close();
      }}
    >
      <div className="settings-tabs backup-tabs">
        {(["backup", "restore"] as const).map((value) => (
          <button
            key={value}
            className={tab === value ? "active" : ""}
            disabled={task.busy}
            onClick={() => {
              setTab(value);
              setKeyForm(false);
              resetPreview();
            }}
          >
            {value === "backup" ? msg("백업") : msg("복원")}
          </button>
        ))}
      </div>
      <ErrorText error={task.error} />
      {tab === "backup" ? (
        <>
          <div className="inline-warning">
            {msg(
              "모든 사용자의 자료와 2단계 인증 복원용 키를 포함하고, 파일 전체를 백업 비밀번호로 암호화해요. 별도 키 파일은 필요하지 않아요.",
            )}
          </div>
          <p className="setting-explainer">
            {msg(
              "백업 비밀번호를 설정하면 하루마다 자동 백업하고 최근 7개를 보관해요. 수동 백업도 정리 대상이므로 필요한 파일은 다른 장치에 내려받아 주세요.",
            )}
          </p>
          <section className="subform">
            <h3>{msg("백업 비밀번호")}</h3>
            <p className="setting-explainer">
              {msg(
                "한 번 설정하면 자동 백업에도 사용해요. 비밀번호는 1Password 같은 안전한 곳에 보관해 주세요. 분실하면 새 서버에서 복원할 수 없어요.",
              )}
            </p>
            <p className="setting-explainer">
              {msg(
                "비밀번호를 바꿔도 기존 백업은 바뀌지 않아요. 이전 백업에는 당시 비밀번호가 필요해요.",
              )}
            </p>
            {!configured || settingsForm ? (
              <form onSubmit={saveBackupPassword}>
                <label className="form-label" htmlFor="new-backup-password">
                  {msg("새 백업 비밀번호")}
                </label>
                <PasswordInput
                  id="new-backup-password"
                  value={backupPassword}
                  onChange={(e) => setBackupPassword(e.target.value)}
                  autoComplete="new-password"
                  minLength={12}
                  required
                />
                <label className="form-label" htmlFor="confirm-backup-password">
                  {msg("백업 비밀번호 확인")}
                </label>
                <PasswordInput
                  id="confirm-backup-password"
                  value={backupPasswordConfirm}
                  onChange={(e) => setBackupPasswordConfirm(e.target.value)}
                  autoComplete="new-password"
                  minLength={12}
                  required
                />
                <AuthenticationFields {...auth} idPrefix="backup-settings" />
                <button
                  className="primary-button entry-submit"
                  disabled={task.busy}
                >
                  <Busy busy={task.busy}>{msg("백업 비밀번호 저장")}</Busy>
                </button>
                {configured && (
                  <button
                    type="button"
                    className="text-link"
                    disabled={task.busy}
                    onClick={() => {
                      setSettingsForm(false);
                      setBackupPassword("");
                      setBackupPasswordConfirm("");
                      setPassword("");
                      setCode("");
                    }}
                  >
                    {msg("취소")}
                  </button>
                )}
              </form>
            ) : (
              <button
                className="secondary-button"
                disabled={task.busy}
                onClick={() => {
                  setSettingsForm(true);
                  setKeyForm(false);
                  setPassword("");
                  setCode("");
                }}
              >
                {msg("백업 비밀번호 변경")}
              </button>
            )}
          </section>
          <button
            className="primary-button entry-submit"
            disabled={task.busy || !configured}
            onClick={() =>
              task.run(async () => {
                const result = await post<{ name: string }>("/backups");
                await load();
                await download(`/backups/${result.name}`, result.name);
                p.notify(msg("백업을 만들고 파일 검증을 마쳤어요."));
              })
            }
          >
            <Busy busy={task.busy}>{msg("백업 만들고 다운로드")}</Busy>
          </button>
          <details className="subform">
            <summary>{msg("기존 ZIP 백업용 키 파일")}</summary>
            <div className="management-row">
              <div>
                <h3>{msg("복원용 키 파일")}</h3>
                <p>
                  {msg(
                    "기존 ZIP 백업에만 원래 master.key가 필요해요. 기존 백업을 보관한다면 키도 함께 보관해 주세요.",
                  )}
                </p>
              </div>
              <button
                className="secondary-button"
                disabled={task.busy}
                onClick={() => {
                  setKeyForm(!keyForm);
                  setSettingsForm(false);
                  setPassword("");
                  setCode("");
                }}
              >
                {msg("키 파일 다운로드")}
              </button>
            </div>
            {keyForm && (
              <form onSubmit={saveKey}>
                <AuthenticationFields {...auth} />
                <button
                  className="primary-button entry-submit"
                  disabled={task.busy}
                >
                  <Busy busy={task.busy}>{msg("확인 후 다운로드")}</Busy>
                </button>
              </form>
            )}
          </details>
          <section className="subform">
            <h3>{msg("저장된 백업")}</h3>
            {backups.map((b) => (
              <div className="management-row backup-row" key={b.name}>
                <div>
                  <strong>
                    {new Date(b.created_at).toLocaleString(intlLocale())}
                  </strong>
                  <p>
                    {b.before_restore && <>{msg("복원 직전 자동 백업")} · </>}
                    {b.encrypted
                      ? msg("암호화 백업")
                      : msg("기존 ZIP 백업")} ·{" "}
                    {(b.size / 1024 / 1024).toFixed(2)} MB
                  </p>
                </div>
                <div className="backup-actions">
                  <button
                    className="secondary-button"
                    disabled={task.busy}
                    aria-label={msg("백업 다운로드")}
                    onClick={() =>
                      task.run(() => download(`/backups/${b.name}`, b.name))
                    }
                  >
                    <Icon name="download" />
                  </button>
                  <button
                    className="secondary-button"
                    disabled={task.busy}
                    onClick={() => {
                      setSource(b.name);
                      setRestorePassword("");
                      setKey(undefined);
                      setTab("restore");
                      resetPreview();
                    }}
                  >
                    {msg("복원")}
                  </button>
                </div>
              </div>
            ))}
            {!backups.length && (
              <p className="setting-explainer">{msg("아직 백업이 없어요.")}</p>
            )}
          </section>
        </>
      ) : (
        <>
          <div className="inline-warning">
            <strong>
              {msg("모든 사용자의 자료가 백업 시점으로 바뀝니다.")}
            </strong>
            <br />
            {msg(
              "복원 전 현재 자료를 자동 백업해요. 복원이 끝나면 모두 다시 로그인해야 하며, 기존 초대 링크와 API 앱 비밀번호는 사용할 수 없어요.",
            )}
          </div>
          {!preview ? (
            <form className="backup-restore-form" onSubmit={inspect}>
              <label className="form-label" htmlFor="restore-source">
                {msg("복원할 백업")}
              </label>
              <select
                id="restore-source"
                className="field-select"
                value={source}
                onChange={(e) => {
                  setSource(e.target.value);
                  setRestorePassword("");
                  setKey(undefined);
                }}
                disabled={task.busy}
              >
                <option value="">{msg("내 장치에서 백업 선택")}</option>
                {backups.map((b) => (
                  <option value={b.name} key={b.name}>
                    {new Date(b.created_at).toLocaleString(intlLocale())}
                    {b.before_restore ? " · " + msg("복원 직전 자동 백업") : ""}
                  </option>
                ))}
              </select>
              {!source && (
                <label className="design-file-box backup-file">
                  <Icon name="plus" />
                  <span>
                    {archive?.name || msg("백업 파일 첨부 (최대 512MB)")}
                  </span>
                  <input
                    type="file"
                    className="visually-hidden"
                    accept=".age,.zip,application/zip,application/octet-stream"
                    aria-label={msg("백업 파일")}
                    disabled={task.busy}
                    onChange={(e) => {
                      const file = e.target.files?.[0];
                      if (file && file.size > 512 * 1024 * 1024) {
                        task.setError(
                          msg(
                            "백업 파일은 512MB 이하, 기존 키 파일은 32바이트여야 해요.",
                          ),
                        );
                        setArchive(undefined);
                      } else {
                        setArchive(file);
                        task.setError("");
                      }
                    }}
                  />
                </label>
              )}
              <label className="form-label" htmlFor="restore-backup-password">
                {msg("백업 비밀번호")}
              </label>
              <PasswordInput
                id="restore-backup-password"
                value={restorePassword}
                onChange={(e) => setRestorePassword(e.target.value)}
                autoComplete="off"
                required={(source || archive?.name || "").endsWith(".age")}
              />
              {!configured && (
                <p className="inline-warning">
                  {msg(
                    "복원 직전 자동 백업을 위해 백업 탭에서 먼저 비밀번호를 설정해 주세요.",
                  )}
                </p>
              )}
              <details>
                <summary>{msg("기존 ZIP 백업의 원래 키 파일")}</summary>
                <label className="design-file-box backup-file">
                  <Icon name="plus" />
                  <span>{key?.name || msg("master.key 첨부")}</span>
                  <input
                    type="file"
                    className="visually-hidden"
                    accept=".key"
                    aria-label="master.key"
                    disabled={task.busy}
                    onChange={(e) => {
                      setKey(e.target.files?.[0]);
                    }}
                  />
                </label>
                {key && (
                  <button
                    type="button"
                    className="text-link"
                    disabled={task.busy}
                    onClick={() => setKey(undefined)}
                  >
                    {msg("키 파일 선택 해제")}
                  </button>
                )}
              </details>
              <p className="setting-explainer">
                {msg(
                  "새 백업은 파일과 백업 비밀번호로 복원해요. 기존 ZIP은 같은 서버의 키를 자동으로 사용해요. 512MB보다 큰 파일이나 로그인할 수 없는 경우에는 매뉴얼의 명령어 복원을 이용해 주세요.",
                )}
              </p>
              <button
                className="primary-button entry-submit"
                disabled={task.busy || !configured || (!source && !archive)}
              >
                <Busy busy={task.busy}>{msg("백업 확인하기")}</Busy>
              </button>
            </form>
          ) : (
            <form onSubmit={restore} className="backup-restore-form">
              <h3>{msg("복원할 자료 확인")}</h3>
              <p>{new Date(preview.created_at).toLocaleString(intlLocale())}</p>
              <dl className="backup-summary">
                <div>
                  <dt>{msg("사용자")}</dt>
                  <dd>{preview.users}</dd>
                </div>
                <div>
                  <dt>{msg("계좌")}</dt>
                  <dd>{preview.accounts}</dd>
                </div>
                <div>
                  <dt>{msg("거래")}</dt>
                  <dd>{preview.transactions}</dd>
                </div>
                <div>
                  <dt>{msg("영수증")}</dt>
                  <dd>{preview.receipts}</dd>
                </div>
              </dl>
              <p className="setting-explainer">
                {msg(
                  "확인한 자료는 10분 동안 복원할 수 있어요. 2단계 인증은 유지되며 현재 서버의 내부 키로 다시 보호해요.",
                )}
              </p>
              <AuthenticationFields {...auth} idPrefix="backup-restore" />
              <label className="backup-confirm">
                <input
                  type="checkbox"
                  checked={confirmed}
                  onChange={(e) => setConfirmed(e.target.checked)}
                  required
                />
                {msg("모든 사용자 자료가 바뀌는 것을 확인했어요.")}
              </label>
              <div className="backup-actions">
                <button
                  type="button"
                  className="secondary-button"
                  disabled={task.busy}
                  onClick={resetPreview}
                >
                  {msg("다시 선택")}
                </button>
                <button
                  className="primary-button"
                  disabled={task.busy || !confirmed}
                >
                  <Busy busy={task.busy}>{msg("현재 자료 백업 후 복원")}</Busy>
                </button>
              </div>
            </form>
          )}
        </>
      )}
    </Modal>
  );
}
