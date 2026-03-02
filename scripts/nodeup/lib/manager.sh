#!/usr/bin/env bash

if [ "${NODEUP_MANAGER_SH_LOADED:-0}" = "1" ]; then
  return 0
fi
NODEUP_MANAGER_SH_LOADED=1

nodeup_is_supported_manager() {
  case "$1" in
    apt|dnf|yum|pacman|zypper)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

nodeup_detect_linux_manager() {
  if nodeup_command_exists apt-get; then
    printf '%s\n' "apt"
    return 0
  fi

  if nodeup_command_exists dnf; then
    printf '%s\n' "dnf"
    return 0
  fi

  if nodeup_command_exists yum; then
    printf '%s\n' "yum"
    return 0
  fi

  if nodeup_command_exists pacman; then
    printf '%s\n' "pacman"
    return 0
  fi

  if nodeup_command_exists zypper; then
    printf '%s\n' "zypper"
    return 0
  fi

  return 1
}

nodeup_package_extension_for_manager() {
  case "$1" in
    apt)
      printf '%s\n' "deb"
      ;;
    dnf|yum|zypper)
      printf '%s\n' "rpm"
      ;;
    pacman)
      printf '%s\n' "pkg.tar.zst"
      ;;
    *)
      return 1
      ;;
  esac
}

nodeup_select_install_method() {
  local os="$1"
  local brew_present="$2"
  local linux_manager="$3"
  local can_manage_packages="$4"

  if [ "$os" = "darwin" ]; then
    if [ "$brew_present" = "1" ]; then
      printf '%s\n' "homebrew"
    else
      printf '%s\n' "binary"
    fi
    return 0
  fi

  if [ "$os" = "linux" ]; then
    if [ -n "$linux_manager" ] && [ "$can_manage_packages" = "1" ]; then
      printf '%s\n' "package"
    else
      printf '%s\n' "binary"
    fi
    return 0
  fi

  return 1
}

nodeup_manager_install_preview() {
  local manager="$1"
  local package_path="$2"

  case "$manager" in
    apt)
      printf '%s\n' "apt-get install -y ${package_path}"
      ;;
    dnf)
      printf '%s\n' "dnf install -y ${package_path}"
      ;;
    yum)
      printf '%s\n' "yum install -y ${package_path}"
      ;;
    pacman)
      printf '%s\n' "pacman -U --noconfirm ${package_path}"
      ;;
    zypper)
      printf '%s\n' "zypper --non-interactive install --allow-unsigned-rpm ${package_path}"
      ;;
    *)
      return 1
      ;;
  esac
}

nodeup_manager_uninstall_preview() {
  local manager="$1"

  case "$manager" in
    apt)
      printf '%s\n' "apt-get purge -y nodeup"
      ;;
    dnf)
      printf '%s\n' "dnf remove -y nodeup"
      ;;
    yum)
      printf '%s\n' "yum remove -y nodeup"
      ;;
    pacman)
      printf '%s\n' "pacman -Rns --noconfirm nodeup"
      ;;
    zypper)
      printf '%s\n' "zypper --non-interactive remove nodeup"
      ;;
    *)
      return 1
      ;;
  esac
}

nodeup_install_package_with_manager() {
  local manager="$1"
  local package_path="$2"

  case "$manager" in
    apt)
      nodeup_run_with_optional_sudo apt-get install -y "$package_path"
      ;;
    dnf)
      nodeup_run_with_optional_sudo dnf install -y "$package_path"
      ;;
    yum)
      nodeup_run_with_optional_sudo yum install -y "$package_path"
      ;;
    pacman)
      nodeup_run_with_optional_sudo pacman -U --noconfirm "$package_path"
      ;;
    zypper)
      nodeup_run_with_optional_sudo zypper --non-interactive install --allow-unsigned-rpm "$package_path"
      ;;
    *)
      return 1
      ;;
  esac
}

nodeup_uninstall_package_with_manager() {
  local manager="$1"

  case "$manager" in
    apt)
      nodeup_run_with_optional_sudo apt-get purge -y nodeup
      ;;
    dnf)
      nodeup_run_with_optional_sudo dnf remove -y nodeup
      ;;
    yum)
      nodeup_run_with_optional_sudo yum remove -y nodeup
      ;;
    pacman)
      nodeup_run_with_optional_sudo pacman -Rns --noconfirm nodeup
      ;;
    zypper)
      nodeup_run_with_optional_sudo zypper --non-interactive remove nodeup
      ;;
    *)
      return 1
      ;;
  esac
}

nodeup_is_package_installed() {
  local manager="$1"

  case "$manager" in
    apt)
      nodeup_command_exists dpkg && dpkg -s nodeup >/dev/null 2>&1
      ;;
    dnf|yum|zypper)
      nodeup_command_exists rpm && rpm -q nodeup >/dev/null 2>&1
      ;;
    pacman)
      nodeup_command_exists pacman && pacman -Q nodeup >/dev/null 2>&1
      ;;
    *)
      return 1
      ;;
  esac
}
