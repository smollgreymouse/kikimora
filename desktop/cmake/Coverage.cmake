# SPDX-License-Identifier: MIT
#
# Coverage.cmake — native C++ source coverage for the desktop UI (U11,
# docs/roadmap.md §18.4, docs/desktop-ui-executor-coverage-plan-2026-11-11.md §14).
#
# Opt-in only: a normal build must not pay for instrumentation. Enable with
#
#     -DKIKIMORA_ENABLE_COVERAGE=ON
#
# Scope is desktop/src/**; generated Qt code, tests and build output are
# excluded. Thresholds are enforced by tests/run-coverage.sh with gcovr — never
# lowered to absorb a red build.

option(KIKIMORA_ENABLE_COVERAGE
       "Instrument the desktop UI for C++ source coverage (U11)" OFF)

if(KIKIMORA_ENABLE_COVERAGE)
    if(NOT CMAKE_CXX_COMPILER_ID MATCHES "GNU|Clang")
        message(FATAL_ERROR
            "KIKIMORA_ENABLE_COVERAGE requires GCC or Clang (gcov/llvm-cov); "
            "current compiler is ${CMAKE_CXX_COMPILER_ID}")
    endif()

    # --coverage is equivalent to -fprofile-arcs -ftest-coverage; branching
    # coverage needs the arc counters, so it is the single flag to add.
    add_compile_options(--coverage -O0 -g)
    add_link_options(--coverage)

    message(STATUS "KIKIMORA_ENABLE_COVERAGE=ON: desktop UI instrumented with --coverage")
endif()
