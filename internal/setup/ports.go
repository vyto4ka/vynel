package setup

import (
	"fmt"
	"net"
	"strings"
)

// CheckPortsFree makes sure the TCP ports vynel's nginx will bind (80 for the site and the
// Let's Encrypt HTTP-01 challenge, 443 for TLS) are free. A system nginx or apache holding
// them is the classic reason a fresh install hangs silently at the certificate step.
func CheckPortsFree() error {
	return checkPortsFree(80, 443)
}

// checkPortsFree is CheckPortsFree with the ports as a parameter, so tests can use
// ephemeral ones instead of the real 80/443.
func checkPortsFree(ports ...int) error {
	var busy []int
	for _, p := range ports {
		l, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", p))
		if err != nil {
			busy = append(busy, p)
			continue
		}
		l.Close() //nolint:gosec // opened only to check it is free
	}
	if len(busy) == 0 {
		return nil
	}
	var what []string
	for _, p := range busy {
		what = append(what, fmt.Sprint(p))
	}
	return fmt.Errorf("порт %s занят другим процессом — vynel нужен сам и 80, и 443 (сайт, подписки, сертификаты Let's Encrypt)."+
		" Обычно это системный nginx или apache: systemctl stop nginx apache2 (и systemctl disable nginx apache2, чтобы не поднялся после перезагрузки), затем запустите установку заново",
		strings.Join(what, " и порт "))
}
