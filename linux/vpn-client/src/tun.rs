use async_trait::async_trait;
use std::ffi::CString;
use std::fs::OpenOptions;
use std::io;
use std::os::fd::{AsRawFd, FromRawFd, OwnedFd};
use std::os::unix::fs::OpenOptionsExt;
use std::process::Command;
use tokio::io::unix::AsyncFd;

const TUNSETIFF: libc::c_ulong = 0x400454ca;
const IFF_TUN: libc::c_short = 0x0001;
const IFF_NO_PI: libc::c_short = 0x1000;

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct TunIdentity {
    pub name: String,
    pub ifindex: u32,
    pub mtu: u16,
}

pub trait TunDevice: Send {
    fn identity(&self) -> TunIdentity;
    fn split(self: Box<Self>) -> (Box<dyn TunReader>, Box<dyn TunWriter>);
}

#[async_trait]
pub trait TunReader: Send {
    async fn read_packet(&mut self, buffer: &mut [u8]) -> io::Result<usize>;
}

#[async_trait]
pub trait TunWriter: Send {
    async fn write_packet(&mut self, packet: &[u8]) -> io::Result<()>;
}

#[repr(C)]
union IfReqData {
    flags: libc::c_short,
    mtu: libc::c_int,
    addr: libc::sockaddr,
    data: *mut libc::c_void,
    pad: [u8; 24],
}

#[repr(C)]
struct IfReq {
    name: [libc::c_char; libc::IFNAMSIZ],
    data: IfReqData,
}

pub struct LinuxTun {
    reader: AsyncFd<OwnedFd>,
    writer: AsyncFd<OwnedFd>,
    identity: TunIdentity,
}

struct LinuxTunReader {
    fd: AsyncFd<OwnedFd>,
}

struct LinuxTunWriter {
    fd: AsyncFd<OwnedFd>,
}

impl LinuxTun {
    pub fn create(name: &str, addresses: &[String], mtu: u16) -> io::Result<Self> {
        let file = OpenOptions::new()
            .read(true)
            .write(true)
            .custom_flags(libc::O_NONBLOCK | libc::O_CLOEXEC)
            .open("/dev/net/tun")?;

        let mut request = IfReq {
            name: [0; libc::IFNAMSIZ],
            data: IfReqData { pad: [0; 24] },
        };
        for (dst, src) in request
            .name
            .iter_mut()
            .zip(name.as_bytes().iter().copied())
        {
            *dst = src as libc::c_char;
        }
        request.data.flags = IFF_TUN | IFF_NO_PI;

        let rc = unsafe { libc::ioctl(file.as_raw_fd(), TUNSETIFF, &mut request) };
        if rc < 0 {
            return Err(io::Error::last_os_error());
        }

        let actual_name = ifreq_name(&request)?;
        configure_link(&actual_name, addresses, mtu)?;
        let c_name = CString::new(actual_name.as_str())
            .map_err(|_| io::Error::new(io::ErrorKind::InvalidInput, "interface contains NUL"))?;
        let ifindex = unsafe { libc::if_nametoindex(c_name.as_ptr()) };
        if ifindex == 0 {
            return Err(io::Error::last_os_error());
        }

        let duplicated = unsafe { libc::fcntl(file.as_raw_fd(), libc::F_DUPFD_CLOEXEC, 0) };
        if duplicated < 0 {
            return Err(io::Error::last_os_error());
        }
        let writer_fd = unsafe { OwnedFd::from_raw_fd(duplicated) };
        let reader_fd: OwnedFd = file.into();

        Ok(Self {
            reader: AsyncFd::new(reader_fd)?,
            writer: AsyncFd::new(writer_fd)?,
            identity: TunIdentity {
                name: actual_name,
                ifindex,
                mtu,
            },
        })
    }
}

impl TunDevice for LinuxTun {
    fn identity(&self) -> TunIdentity {
        self.identity.clone()
    }

    fn split(self: Box<Self>) -> (Box<dyn TunReader>, Box<dyn TunWriter>) {
        (
            Box::new(LinuxTunReader { fd: self.reader }),
            Box::new(LinuxTunWriter { fd: self.writer }),
        )
    }
}

#[async_trait]
impl TunReader for LinuxTunReader {
    async fn read_packet(&mut self, buffer: &mut [u8]) -> io::Result<usize> {
        loop {
            let mut guard = self.fd.readable().await?;
            match guard.try_io(|inner| {
                let rc = unsafe {
                    libc::read(
                        inner.get_ref().as_raw_fd(),
                        buffer.as_mut_ptr().cast(),
                        buffer.len(),
                    )
                };
                if rc < 0 {
                    Err(io::Error::last_os_error())
                } else {
                    Ok(rc as usize)
                }
            }) {
                Ok(result) => return result,
                Err(_) => continue,
            }
        }
    }
}

#[async_trait]
impl TunWriter for LinuxTunWriter {
    async fn write_packet(&mut self, packet: &[u8]) -> io::Result<()> {
        loop {
            let mut guard = self.fd.writable().await?;
            match guard.try_io(|inner| {
                let rc = unsafe {
                    libc::write(
                        inner.get_ref().as_raw_fd(),
                        packet.as_ptr().cast(),
                        packet.len(),
                    )
                };
                if rc < 0 {
                    return Err(io::Error::last_os_error());
                }
                if rc as usize != packet.len() {
                    return Err(io::Error::new(
                        io::ErrorKind::WriteZero,
                        "short write to TUN device",
                    ));
                }
                Ok(())
            }) {
                Ok(result) => return result,
                Err(_) => continue,
            }
        }
    }
}

fn ifreq_name(request: &IfReq) -> io::Result<String> {
    let bytes: Vec<u8> = request
        .name
        .iter()
        .take_while(|c| **c != 0)
        .map(|c| *c as u8)
        .collect();
    String::from_utf8(bytes)
        .map_err(|_| io::Error::new(io::ErrorKind::InvalidData, "kernel returned invalid interface name"))
}

fn configure_link(name: &str, addresses: &[String], mtu: u16) -> io::Result<()> {
    let ip = std::env::var("KIKIMORA_VPN_IP_COMMAND").unwrap_or_else(|_| "ip".to_string());
    let mtu_string = mtu.to_string();

    run_ip(&ip, &["link", "set", "dev", name, "mtu", &mtu_string])?;
    for address in addresses {
        run_ip(&ip, &["address", "add", address, "dev", name])?;
    }
    run_ip(&ip, &["link", "set", "dev", name, "up"])?;
    Ok(())
}

fn run_ip(program: &str, args: &[&str]) -> io::Result<()> {
    let output = Command::new(program).args(args).output()?;
    if output.status.success() {
        return Ok(());
    }
    Err(io::Error::other(format!(
        "{} {} failed: {}",
        program,
        args.join(" "),
        String::from_utf8_lossy(&output.stderr).trim()
    )))
}
