#!/usr/bin/env bash

# Shared parsing for the component lists produced by
# detect-deployment-components.sh and consumed by build, scan, and deploy.

declare -ag DEPLOYMENT_COMPONENT_ORDER=(
  infrastructure
  migration
  backend
  frontend
  application
  observability
  gateway
)
declare -Ag DEPLOYMENT_SELECTED_COMPONENTS=()

deployment_components_add() {
  local component="$1"

  case "${component}" in
    infrastructure|migration|application|observability|gateway)
      DEPLOYMENT_SELECTED_COMPONENTS["${component}"]=1
      ;;
    backend)
      DEPLOYMENT_SELECTED_COMPONENTS[backend]=1
      DEPLOYMENT_SELECTED_COMPONENTS[migration]=1
      DEPLOYMENT_SELECTED_COMPONENTS[application]=1
      ;;
    frontend)
      DEPLOYMENT_SELECTED_COMPONENTS[frontend]=1
      DEPLOYMENT_SELECTED_COMPONENTS[application]=1
      ;;
    *)
      return 1
      ;;
  esac
}

deployment_components_select() {
  local selection="$1"
  local consumer="${2:-deployment}"
  local component
  local requested_components=()

  DEPLOYMENT_SELECTED_COMPONENTS=()

  if [[ ! "${selection}" =~ ^[a-z]+(,[a-z]+)*$ ]]; then
    echo "Invalid ${consumer} component list: ${selection}" >&2
    return 1
  fi

  case "${selection}" in
    all)
      for component in "${DEPLOYMENT_COMPONENT_ORDER[@]}"; do
        deployment_components_add "${component}"
      done
      ;;
    none)
      ;;
    *)
      IFS=',' read -r -a requested_components <<<"${selection}"
      for component in "${requested_components[@]}"; do
        if ! deployment_components_add "${component}"; then
          echo "Unknown ${consumer} component: ${component}" >&2
          return 1
        fi
      done
      ;;
  esac
}

deployment_component_selected() {
  [[ "${DEPLOYMENT_SELECTED_COMPONENTS[$1]:-0}" == "1" ]]
}

deployment_components_any_selected() {
  ((${#DEPLOYMENT_SELECTED_COMPONENTS[@]} > 0))
}
