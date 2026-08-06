"use strict";

const HOST_NAME = "com.kikimora.domain_manager";
const DOMAIN_RE = /^(?:\*\.)?(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/;
const ZONES = ["primary", "secondary"];

const form = document.querySelector("#domain-form");
const domainInput = document.querySelector("#domain");
const submitButton = document.querySelector("#submit");
const statusNode = document.querySelector("#status");
const listStatusNode = document.querySelector("#list-status");
const filterInput = document.querySelector("#filter");
const refreshButton = document.querySelector("#refresh");

const lists = {
  primary: document.querySelector("#primary-domains"),
  secondary: document.querySelector("#secondary-domains")
};
const counts = {
  primary: document.querySelector("#primary-count"),
  secondary: document.querySelector("#secondary-count")
};

let domainsByZone = {primary: [], secondary: []};

function setStatus(node, message, kind = "") {
  node.textContent = message;
  node.className = `status ${kind}`.trim();
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

function currentInputDomain() {
  try {
    return normalizeDomain(domainInput.value);
  } catch (_error) {
    return "";
  }
}

function nativeHostHint(error) {
  const message = error instanceof Error ? error.message : String(error);
  const hostMissing = message.includes("native messaging host") ||
    message.includes("Specified native messaging host");
  return hostMissing
    ? "Native host Kikimora не установлен. Запустите browser/chrome/install.sh."
    : message;
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
    throw new Error("Откройте обычную HTTP/HTTPS-страницу или введите домен вручную");
  }
  domainInput.value = url.hostname.toLowerCase();
  renderDomainLists();
}

async function restoreZone() {
  const {lastZone = "primary"} = await chrome.storage.local.get("lastZone");
  const radio = document.querySelector(`input[name="zone"][value="${lastZone}"]`);
  if (radio) radio.checked = true;
}

function makeEmptyRow(message) {
  const item = document.createElement("li");
  item.className = "empty-row";
  item.textContent = message;
  return item;
}

function renderZone(zone) {
  const list = lists[zone];
  const allDomains = domainsByZone[zone];
  const filter = filterInput.value.trim().toLowerCase();
  const visibleDomains = filter
    ? allDomains.filter((domain) => domain.includes(filter))
    : allDomains;
  const activeDomain = currentInputDomain();

  list.replaceChildren();
  counts[zone].textContent = filter
    ? `${visibleDomains.length}/${allDomains.length}`
    : String(allDomains.length);

  if (visibleDomains.length === 0) {
    list.append(makeEmptyRow(filter ? "Совпадений нет" : "Список пуст"));
    return;
  }

  for (const domain of visibleDomains) {
    const item = document.createElement("li");
    item.className = "domain-row";
    if (domain === activeDomain) item.classList.add("current");

    const name = document.createElement("span");
    name.className = "domain-name";
    name.textContent = domain;
    name.title = domain;

    const removeButton = document.createElement("button");
    removeButton.type = "button";
    removeButton.className = "delete-button";
    removeButton.textContent = "Удалить";
    removeButton.dataset.domain = domain;
    removeButton.dataset.zone = zone;
    removeButton.setAttribute("aria-label", `Удалить ${domain} из ${zone}`);
    removeButton.addEventListener("click", removeDomain);

    item.append(name, removeButton);
    list.append(item);
  }
}

function renderDomainLists() {
  for (const zone of ZONES) renderZone(zone);
}

async function loadDomains({silent = false} = {}) {
  refreshButton.disabled = true;
  if (!silent) setStatus(listStatusNode, "Загрузка списков…");

  try {
    const response = await sendNativeMessage({action: "list-domains"});
    if (!response.ok) {
      throw new Error(response.error || "Kikimora не вернула списки доменов");
    }

    domainsByZone = {
      primary: Array.isArray(response.zones?.primary) ? response.zones.primary : [],
      secondary: Array.isArray(response.zones?.secondary) ? response.zones.secondary : []
    };
    renderDomainLists();
    if (!silent) setStatus(listStatusNode, "Списки обновлены", "success");
  } catch (error) {
    setStatus(listStatusNode, nativeHostHint(error), "error");
  } finally {
    refreshButton.disabled = false;
  }
}

async function removeDomain(event) {
  const button = event.currentTarget;
  const domain = button.dataset.domain;
  const zone = button.dataset.zone;
  if (!domain || !ZONES.includes(zone)) return;

  if (!window.confirm(`Удалить ${domain} из ${zone}?`)) return;

  button.disabled = true;
  setStatus(statusNode, `Удаление ${domain}…`);

  try {
    const response = await sendNativeMessage({
      action: "remove-domain",
      domain,
      zone
    });
    if (!response.ok) {
      throw new Error(response.error || "Kikimora отклонила удаление");
    }

    setStatus(statusNode, response.message || `Удалено из ${zone}`, "success");
    await loadDomains({silent: true});
  } catch (error) {
    setStatus(statusNode, nativeHostHint(error), "error");
    button.disabled = false;
  }
}

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  setStatus(statusNode, "");

  let domain;
  try {
    domain = normalizeDomain(domainInput.value);
  } catch (error) {
    setStatus(statusNode, error.message, "error");
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
    setStatus(statusNode, response.message || `Добавлено в ${zone}`, "success");
    await loadDomains({silent: true});
  } catch (error) {
    setStatus(statusNode, nativeHostHint(error), "error");
  } finally {
    submitButton.disabled = false;
    submitButton.textContent = "Добавить домен";
  }
});

filterInput.addEventListener("input", renderDomainLists);
domainInput.addEventListener("input", renderDomainLists);
refreshButton.addEventListener("click", () => loadDomains());

restoreZone().catch((error) => setStatus(statusNode, error.message, "error"));
loadCurrentTab().catch((error) => setStatus(statusNode, error.message, "error"));
loadDomains();
