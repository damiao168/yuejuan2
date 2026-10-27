use super::*;
use std::{
    process::Command,
    thread,
    time::{Duration, Instant},
};

const CHILD_ROOT: &str = "EDUGRADE_SYNTHETIC_SPOOL_RACE_ROOT";
const CHILD_ID: &str = "EDUGRADE_SYNTHETIC_SPOOL_RACE_CHILD";
const KEY: [u8; 32] = [29; 32];

// 子进程只使用父测试新建的 SQLite、合成字节与固定测试密钥，不访问系统凭据或真实队列。
#[test]
fn chunk_race_child() {
    let Some(root) = std::env::var_os(CHILD_ROOT) else {
        return;
    };
    let root = PathBuf::from(root);
    let child = std::env::var(CHILD_ID).unwrap();
    let conn = open_connection_at(&root).unwrap();
    conn.busy_timeout(Duration::from_secs(10)).unwrap();
    let result = write_spool_local_asset_chunk_at(
        &root,
        &KEY,
        &conn,
        "synthetic-race".into(),
        0,
        b"abc".to_vec(),
    );
    fs::write(
        root.join(format!("result-{child}")),
        if result.is_ok() {
            "ok".into()
        } else {
            format!("error: {}", result.unwrap_err())
        },
    )
    .unwrap();
}

#[test]
fn independent_processes_preserve_the_confirmed_chunk() {
    let root =
        std::env::temp_dir().join(format!("edugrade-synthetic-spool-race-{}", Uuid::new_v4()));
    let directory = spool_path(&root).join("synthetic-race");
    fs::create_dir_all(&directory).unwrap();
    let conn = open_connection_at(&root).unwrap();
    initialize_schema(&conn).unwrap();
    let now = now_rfc3339();
    conn.execute("INSERT INTO local_asset(id,sha256,size_bytes,mime,local_path,file_nonce,state,created_at,updated_at) VALUES('synthetic-race',?1,3,'application/pdf',?2,?3,'ingesting',?4,?4)", params![sha256_hex(b"abc"), directory.to_string_lossy(), CHUNKED_FILE_NONCE, now]).unwrap();
    conn.execute("INSERT INTO local_asset_ingest(local_asset_id,idempotency_key,expected_size,received_size,payload_ciphertext,payload_nonce,created_at,updated_at) VALUES('synthetic-race','synthetic-race',3,0,'synthetic','synthetic',?1,?1)", params![now]).unwrap();

    // 持 SQLite 写锁，使两个真实子进程都读到 offset=0 并完成文件落盘，再一起竞争确认。
    conn.execute_batch("BEGIN IMMEDIATE").unwrap();
    let executable = std::env::current_exe().unwrap();
    let mut children = ["a", "b"].map(|child| {
        Command::new(&executable)
            .args([
                "--exact",
                "durable_store::spool::tests::chunk_race_child",
                "--nocapture",
            ])
            .env(CHILD_ROOT, &root)
            .env(CHILD_ID, child)
            .spawn()
            .unwrap()
    });
    let deadline = Instant::now() + Duration::from_secs(5);
    let saw_two_files = loop {
        if fs::read_dir(&directory).unwrap().count() == 2 {
            break true;
        }
        if Instant::now() >= deadline {
            break false;
        }
        thread::sleep(Duration::from_millis(10));
    };
    // 即使边界断言失败也先释放锁并回收进程，避免测试留下仍在写入的临时目录。
    conn.execute_batch("COMMIT").unwrap();
    for child in &mut children {
        assert!(child.wait().unwrap().success());
    }
    assert!(
        saw_two_files,
        "both attempts must own a separate file before database confirmation"
    );
    let outcomes =
        ["a", "b"].map(|child| fs::read_to_string(root.join(format!("result-{child}"))).unwrap());
    assert_eq!(
        outcomes.iter().filter(|value| *value == "ok").count(),
        1,
        "{outcomes:?}"
    );
    let confirmed: i64 = conn
        .query_row(
            "SELECT received_size FROM local_asset_ingest WHERE local_asset_id='synthetic-race'",
            [],
            |row| row.get(0),
        )
        .unwrap();
    assert_eq!(confirmed, 3);
    assert_eq!(fs::read_dir(&directory).unwrap().count(), 1);
    verify_chunked_asset(&conn, &root, &KEY, "synthetic-race", 3, &sha256_hex(b"abc")).unwrap();
    let path: String = conn
        .query_row(
            "SELECT local_path FROM local_asset_chunk WHERE local_asset_id='synthetic-race'",
            [],
            |row| row.get(0),
        )
        .unwrap();
    let before = fs::read(&path).unwrap();
    assert!(write_spool_local_asset_chunk_at(
        &root,
        &KEY,
        &conn,
        "synthetic-race".into(),
        0,
        b"abc".to_vec()
    )
    .is_err());
    assert_eq!(fs::read(&path).unwrap(), before);
    drop(conn);
    fs::remove_dir_all(root).unwrap();
}

#[test]
fn rejected_confirmation_removes_only_its_own_attempt_file() {
    let root = std::env::temp_dir().join(format!(
        "edugrade-synthetic-spool-rollback-{}",
        Uuid::new_v4()
    ));
    let directory = spool_path(&root).join("synthetic-race");
    fs::create_dir_all(&directory).unwrap();
    let conn = open_connection_at(&root).unwrap();
    initialize_schema(&conn).unwrap();
    let now = now_rfc3339();
    conn.execute("INSERT INTO local_asset(id,sha256,size_bytes,mime,local_path,file_nonce,state,created_at,updated_at) VALUES('synthetic-race',?1,3,'application/pdf',?2,?3,'ingesting',?4,?4)", params![sha256_hex(b"abc"), directory.to_string_lossy(), CHUNKED_FILE_NONCE, now]).unwrap();
    conn.execute("INSERT INTO local_asset_ingest(local_asset_id,idempotency_key,expected_size,received_size,payload_ciphertext,payload_nonce,created_at,updated_at) VALUES('synthetic-race','synthetic-race',3,0,'synthetic','synthetic',?1,?1)", params![now]).unwrap();
    let legacy_orphan = directory.join("000000000000.chunk");
    fs::write(&legacy_orphan, b"synthetic crash residue").unwrap();
    conn.execute_batch("CREATE TRIGGER reject_chunk BEFORE INSERT ON local_asset_chunk BEGIN SELECT RAISE(ABORT,'synthetic database failure'); END").unwrap();
    assert!(write_spool_local_asset_chunk_at(
        &root,
        &KEY,
        &conn,
        "synthetic-race".into(),
        0,
        b"abc".to_vec()
    )
    .is_err());
    assert_eq!(
        fs::read(&legacy_orphan).unwrap(),
        b"synthetic crash residue"
    );
    assert_eq!(fs::read_dir(&directory).unwrap().count(), 1);
    conn.execute_batch("DROP TRIGGER reject_chunk").unwrap();
    assert_eq!(
        write_spool_local_asset_chunk_at(
            &root,
            &KEY,
            &conn,
            "synthetic-race".into(),
            0,
            b"abc".to_vec()
        )
        .unwrap(),
        3
    );
    verify_chunked_asset(&conn, &root, &KEY, "synthetic-race", 3, &sha256_hex(b"abc")).unwrap();
    drop(conn);
    fs::remove_dir_all(root).unwrap();
}
