#include <dlfcn.h>
#include <linux/filter.h>
#include <linux/seccomp.h>
#include <stddef.h>
#include <stdio.h>
#include <string.h>
#include <sys/prctl.h>
#include <sys/syscall.h>

int main(int argc, char **argv) {
    if (argc != 3) return 2;
    if (strcmp(argv[1], "trap") == 0) {
        // Install before dlopen so every Go thread inherits the restriction.
        struct sock_filter filter[] = {
            BPF_STMT(BPF_LD | BPF_W | BPF_ABS, offsetof(struct seccomp_data, nr)),
            BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, __NR_pidfd_open, 0, 1),
            BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_TRAP),
            BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ALLOW),
        };
        struct sock_fprog program = { .len = 4, .filter = filter };
        if (prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) ||
            prctl(PR_SET_SECCOMP, SECCOMP_MODE_FILTER, &program)) {
            perror("seccomp");
            return 2;
        }
    }
    void *lib = dlopen(argv[2], RTLD_NOW);
    if (!lib) {
        fprintf(stderr, "dlopen: %s\n", dlerror());
        return 2;
    }
    int (*run)(void) = dlsym(lib, "Run");
    if (!run) {
        fprintf(stderr, "dlsym: %s\n", dlerror());
        return 2;
    }
    return run();
}
