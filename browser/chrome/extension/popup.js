"use strict";

const HOST_NAME = "com.kikimora.domain_manager";
const DOMAIN_RE = /^(?:\*\.)?(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/;

const form = document.querySelector("#domain-form");
const domainInput = document.querySelector("#domain");
const submitButton = document.querySelector("#submit");
const statusNode = document.querySelector("#status");

function setStatus(message, kind = "") {
  statusNode.textContent = message;
  statusNode.className = `status ${kind}`.trim();
}

function normalizeDomain(value) {
  let domain = value.trim().toLowerCase();

  if (domain.includes("://")) {
    domain = new URL(domain).hostname.toLowerCase();
  } else {
    domain = domain.split(/[/?#]/, 1)[0];
    domain = domain.replace(/:\d+$/, "");
  }

  domain = domain.replace(/\.$/, "");
  if (!DOMAIN_RE.test(domain)) {
    throw new Error("Введите обычное доменное имя, например example.com");
  }
  return domain;
}

function currentZone() {
  return document.querySelector('input[name="zone"]:checked').value;
}

function sendNativeMessage(message) {
  return new Promise((resolve, reject) => {
    chrome.runtime.sendNativeMessage(HOST_NAME, message, (response) => {
      const error = chrome.runtime.lastError;
      if (error) {
        reject(new Error(error.message));
        return;
      }
      if (!response) {
        reject(new Error("Native host не вернул ответ"));
        return;
      }
      resolve(response);
    });
  });
}

async function loadCurrentTab() {
  const [tab] = await chrome.tabs.query({active: true, currentWindow: true});
  if (!tab?.url) {
    throw new Error("Не удалось прочитать адрес текущей вкладки");
  }

  const url = new URL(tab.url);
  if (!["http:", "https:"].includes(url.protocol) || !url.hostname) {
    throw new Error("Откройте обычную HTTP/HTTPS-страницу");
  }
  domainInput.value = url.hostname.toLowerCase();
}

async function restoreZone() {
  const {lastZone = "primary"} = await chrome.storage.local.get("lastZone");
  const radio = document.querySelector(`input[name="zone"][value="${lastZone}"]`);
  if (radio) radio.checked = true;
}

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  setStatus("");

  let domain;
  try {
    domain = normalizeDomain(domainInput.value);
  } catch (error) {
    setStatus(error.message, "error");
    return;
  }

  const zone = currentZone();
  submitButton.disabled = true;
  submitButton.textContent = "Добавление…";

  try {
    await chrome.storage.local.set({lastZone: zone});
    const response = await sendNativeMessage({
      action: "add-domain",
      domain,
      zone
    });

    if (!response.ok) {
      throw new Error(response.error || "Kikimora отклонила запрос");
    }

    domainInput.value = response.domain || domain;
    setStatus(response.message || `Добавлено в ${zone}`, "success");
  } catch (error) {
    const hostHint = error.message.includes("native messaging host") ||
      error.message.includes("Specified native messaging host");
    setStatus(
      hostHint
        ? "Native host Kikimora не установлен. Запустите browser/chrome/install.sh."
        : error.message,
      "error"
    );
  } finally {
    submitButton.disabled = false;
    submitButton.textContent = "Добавить домен";
  }
});

Promise.all([restoreZone(), loadCurrentTab()]).catch((error) => {
  setStatus(error.message, "error");
  submitButton.disabled = true;
});
